// Package loader implements the flexconf loading pipeline: locate → read →
// parse → template → (decode). Templating resolves $(env:…), $(secret:…) and
// $(config:…) references on the parsed node tree (never on raw text), tracking
// secret-sourced nodes as tainted so every config dump can redact them.
// $(secret:…) resolves through the toolkit's existing secrets.Store; which
// driver backs it is chosen by an injected store, a `secrets` block, or the
// zero-config agent→keepass default. The public surface is re-exported from the
// module-root flexconf package; this package is internal.
package loader

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/sylvanld/go-flexconf/secrets"
	"github.com/sylvanld/go-flexconf/settings"
)

// Settings is a config block that has been located, templated, and
// secret-resolved, but not yet decoded into a typed struct — lazily loaded
// settings. It is what LoadFile returns for a whole config file, and it is also
// usable as a struct field (via UnmarshalYAML) so a parent can capture a child
// block without knowing its shape and let the owning subsystem Decode it later.
// A PolymorphicSettings resolves one into a concrete type chosen by a
// discriminator field. Decode unmarshals it into a typed struct; Dump renders it
// back to YAML with secrets redacted.
type Settings struct {
	Tree  *yaml.Node
	Taint NodeSet
}

// Decode unmarshals the templated tree into out (a pointer to the app's config
// struct). The loader-owned `secrets` block has already been removed, so it
// never leaks into the application schema.
func (s *Settings) Decode(out any) error {
	if s == nil || s.Tree == nil {
		return errors.New("flexconf: nothing to decode")
	}
	return s.Tree.Decode(out)
}

// UnmarshalYAML lets a Settings be a field of another struct: it captures the
// (already-templated) node verbatim instead of decoding it, deferring the typed
// decode to whoever owns the block. The captured node carries no taint set, so
// redaction is a top-level concern — Dump on a nested Settings does not redact;
// dump the whole config from the LoadFile result instead.
func (s *Settings) UnmarshalYAML(n *yaml.Node) error {
	s.Tree = n
	s.Taint = NodeSet{}
	return nil
}

// options carries Load's injectable inputs.
type options struct {
	configFile string
	env        Env
	store      *secrets.Store
	resolver   SecretResolver
	fsys       fs.FS
}

// Option customises a Load.
type Option func(*options)

// WithConfigFile sets an explicit config file path (highest precedence). Empty
// is a no-op.
func WithConfigFile(path string) Option {
	return func(o *options) {
		if path != "" {
			o.configFile = path
		}
	}
}

// WithEnv injects the environment used for $(env:…) and the <APP>_CONFIG
// lookup. Defaults to the process environment.
func WithEnv(env Env) Option {
	return func(o *options) {
		if env != nil {
			o.env = env
		}
	}
}

// WithSecretStore injects a ready *secrets.Store as the $(secret:…) resolver,
// bypassing the `secrets` block and the zero-config default.
func WithSecretStore(s *secrets.Store) Option {
	return func(o *options) {
		if s != nil {
			o.store = s
		}
	}
}

// WithSecretResolver injects a SecretResolver directly (tests, custom
// backends). Wins over WithSecretStore.
func WithSecretResolver(r SecretResolver) Option {
	return func(o *options) {
		if r != nil {
			o.resolver = r
		}
	}
}

// WithFS reads the root config and its includes from fsys instead of the OS
// filesystem. The resolved config path is then interpreted relative to fsys.
// Primarily for tests.
func WithFS(fsys fs.FS) Option {
	return func(o *options) {
		if fsys != nil {
			o.fsys = fsys
		}
	}
}

// Load resolves the config file, reads/parses/templates it, and unmarshals the
// result into out. It is LoadFile followed by Decode.
func Load(cfg *settings.AppConfig, out any, opts ...Option) error {
	ld, err := LoadFile(cfg, opts...)
	if err != nil {
		return err
	}
	return ld.Decode(out)
}

// LoadFile runs the locate→read→parse→template pipeline and returns the
// templated tree plus its taint set, without decoding. Use it when you also
// need Dump (redacted rendering) or want to decode strictly yourself.
func LoadFile(cfg *settings.AppConfig, opts ...Option) (*Settings, error) {
	o := options{env: OSEnv{}}
	for _, opt := range opts {
		opt(&o)
	}

	fsys, file, err := o.rootFS(cfg)
	if err != nil {
		return nil, err
	}
	l := &loader{fsys: fsys, env: o.env}

	root, err := l.readAndParse(file)
	if err != nil {
		return nil, err
	}
	tree, taint, err := l.run(cfg, root, file, o)
	if err != nil {
		return nil, err
	}
	return &Settings{Tree: tree, Taint: taint}, nil
}

// rootFS resolves the config path (option → <APP>_CONFIG → cfg default) and
// returns the fs to read from plus the fs-relative name of the root file.
func (o options) rootFS(cfg *settings.AppConfig) (fs.FS, string, error) {
	path, err := o.resolvePath(cfg)
	if err != nil {
		return nil, "", err
	}
	if o.fsys != nil {
		return o.fsys, path, nil
	}
	return os.DirFS(filepath.Dir(path)), filepath.Base(path), nil
}

// resolvePath applies the config-file location precedence.
func (o options) resolvePath(cfg *settings.AppConfig) (string, error) {
	if o.configFile != "" {
		return o.configFile, nil
	}
	if cfg == nil {
		return "", errors.New("flexconf: no config file and no settings supplied")
	}
	if v, ok := o.env.Lookup(envKey(cfg.AppName()) + "_CONFIG"); ok && v != "" {
		return v, nil
	}
	return cfg.File("config.yaml"), nil
}

// loader carries the pipeline's injectable inputs: the fs the root config and
// its includes are read from, and the environment.
type loader struct {
	fsys fs.FS
	env  Env
}

// readAndParse reads one file and unmarshals it into a generic YAML node tree
// — structure only, no typed decoding yet. name is fs-relative.
func (l *loader) readAndParse(name string) (*yaml.Node, error) {
	data, err := fs.ReadFile(l.fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, fmt.Errorf("parse %s: empty config", name)
	}
	return doc.Content[0], nil
}

// run is the templating pipeline in bootstrap order: template the secrets
// block env-only, build the resolver from it, template the rest of the tree
// against that resolver, then strip the loader-owned secrets block from the
// tree before it is handed to the app's decoder.
func (l *loader) run(cfg *settings.AppConfig, root *yaml.Node, file string, o options) (*yaml.Node, NodeSet, error) {
	secretsNode := mappingValue(root, "secrets")
	if secretsNode != nil {
		bt := &templater{l: l, allowSecret: false, stack: []string{file}, tainted: NodeSet{}}
		bt.walk(secretsNode, file)
		if err := errors.Join(bt.errs...); err != nil {
			return nil, nil, err
		}
	}

	resolver, err := l.buildResolver(cfg, secretsNode, o)
	if err != nil {
		return nil, nil, err
	}

	t := &templater{l: l, secrets: resolver, allowSecret: true, stack: []string{file}, tainted: NodeSet{}}
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "secrets" {
				continue // already templated, env-only
			}
			t.walk(root.Content[i+1], file)
		}
	} else {
		t.walk(root, file)
	}
	if err := errors.Join(t.errs...); err != nil {
		return nil, nil, err
	}

	stripKey(root, "secrets")
	return root, t.tainted, nil
}

// buildResolver selects the $(secret:…) resolver: an injected resolver/store
// wins, then the `secrets` block's driver, then the zero-config default.
func (l *loader) buildResolver(cfg *settings.AppConfig, secretsNode *yaml.Node, o options) (SecretResolver, error) {
	if o.resolver != nil {
		return o.resolver, nil
	}
	if o.store != nil {
		return StoreResolver(o.store), nil
	}

	driver := defaultDriver
	var opts *yaml.Node
	if secretsNode != nil {
		var block struct {
			Driver  string               `yaml:"driver"`
			Drivers map[string]yaml.Node `yaml:",inline"`
		}
		if err := strictDecode(secretsNode, &block); err != nil {
			return nil, fmt.Errorf("secrets block: %w", err)
		}
		if block.Driver != "" {
			driver = block.Driver
		}
		if n, ok := block.Drivers[driver]; ok {
			opts = &n
		}
	}

	f, err := secretDriverFactory(driver)
	if err != nil {
		return nil, err
	}
	d, err := f(cfg, opts, l.env)
	if err != nil {
		return nil, fmt.Errorf("secrets.driver %q: %w", driver, err)
	}
	return StoreResolver(secrets.NewStore(d)), nil
}

// mappingValue returns the value node for a key of a mapping, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// stripKey removes a top-level key/value pair from a mapping node.
func stripKey(n *yaml.Node, key string) {
	if n.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			continue
		}
		kept = append(kept, n.Content[i], n.Content[i+1])
	}
	n.Content = kept
}

// strictDecode decodes a YAML node rejecting unknown keys — the fail-loud
// discipline the secrets block and driver options follow. A field inlined as
// map[string]yaml.Node absorbs the remaining keys where a block allows them.
func strictDecode(n *yaml.Node, out any) error {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && err != io.EOF {
		return err
	}
	return nil
}
