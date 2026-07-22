// Package flexconf loads application configuration from layered config
// directories into Go structs, resolving templating tokens along the way.
//
// A Loader is constructed with an ordered list of config directories (layers);
// Load(name, &dst) reads the named YAML file from each layer, merges by
// precedence, resolves $(scheme:path) tokens, binds the result into dst, and
// validates it — failing loud and early at every step.
package flexconf

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/sylvanld/go-flexconf/internal/variant"
)

// Loader reads config files from an ordered list of directories, lowest to
// highest precedence. It is safe for concurrent Load calls.
type Loader struct {
	dirs       []string
	opts       loaderOptions
	registries []variant.Binder // WithRegistry overrides for variant routing
	secrets    *secretState     // per-Loader unlocked-vault cache (shared by With copies)
}

// loaderOptions collects the tunable Loader behaviour; populated by Options.
type loaderOptions struct {
	scoped       map[string]Resolver         // WithResolver overrides
	set          map[string]Resolver         // WithResolvers replacement set
	replaced     bool                        // WithResolvers was called
	env          func(string) (string, bool) // WithEnv source for env:
	fsys         fs.FS                       // WithFS source for file:/config:
	secretPolicy SecretPolicy                // WithSecretPolicy (default PolicyAgent)
}

// Option tunes a Loader (see With).
type Option func(*loaderOptions)

// New returns a Loader that reads config files from dirs, ordered lowest to
// highest precedence (a later dir overrides an earlier one). At least one dir
// is required; passing none is a programming error and New PANICS.
func New(dirs ...string) *Loader {
	if len(dirs) == 0 {
		panic("flexconf: New requires at least one config directory")
	}
	return &Loader{dirs: dirs, secrets: &secretState{}}
}

// With returns a copy of the Loader with the given options applied.
func (l *Loader) With(opts ...Option) *Loader {
	nl := *l
	for _, o := range opts {
		o(&nl.opts)
	}
	return &nl
}

// validateName enforces the Load name rules: a plain file name (single path
// segment, no ".."), with a .yaml or .yml extension.
func validateName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	switch filepath.Ext(name) {
	case ".yaml", ".yml":
		return nil
	default:
		return fmt.Errorf("%w: %q (want .yaml or .yml)", ErrUnsupportedFormat, name)
	}
}

// Load reads the file name from each configured directory, merges the layers
// by precedence, resolves tokens on the merged tree, binds the result into
// dst (a non-nil pointer to a struct), and validates it. It fails with
// ErrConfigNotFound if no layer provides name. On any error dst is left
// exactly as passed (all-or-nothing).
func (l *Loader) Load(name string, dst any) error {
	if err := validateName(name); err != nil {
		return err
	}
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Pointer || dv.IsNil() || dv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("flexconf: Load destination must be a non-nil struct pointer, got %T", dst)
	}
	elem := dv.Elem()

	// 1. Read: locate name in each layer and parse each present copy.
	layers, err := l.readLayers(name)
	if err != nil {
		return err
	}
	if len(layers) == 0 {
		return fmt.Errorf("%w: %q (dirs: %s)", ErrConfigNotFound, name, strings.Join(l.dirs, ", "))
	}

	// 1a. Per-file shape validation against the destination schema.
	for _, layer := range layers {
		if err := validateShape(layer, elem.Type(), ""); err != nil {
			return err
		}
	}

	// 2. Merge by precedence into one raw tree.
	tree := mergeTrees(layers)

	// 3. Resolve $(scheme:path) tokens on the merged tree.
	if err := l.resolveTree(tree); err != nil {
		return err
	}

	// 4+5. Bind + validate into a temp seeded from dst, then swap on success
	// (all-or-nothing).
	temp := reflect.New(elem.Type())
	temp.Elem().Set(elem)
	b := &binder{loader: l}
	if err := b.bindStruct(tree, temp.Elem(), ""); err != nil {
		b.rollback() // discard variant instances registered by this attempt
		return err
	}
	elem.Set(temp.Elem())
	return nil
}

// readLayers reads and parses `name` from every layer directory that contains
// it, in precedence order. A present-but-empty file contributes an empty tree.
func (l *Loader) readLayers(name string) ([]*node, error) {
	var layers []*node
	for _, dir := range l.dirs {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // a layer without the file is simply skipped
		}
		if err != nil {
			return nil, fmt.Errorf("flexconf: reading %s: %w", path, err)
		}
		tree, err := parseYAML(data, path)
		if err != nil {
			return nil, fmt.Errorf("flexconf: %w", err)
		}
		if tree.kind != kindMap {
			return nil, fmt.Errorf("flexconf: %s: top-level config must be a map, found a %s", path, tree.kind)
		}
		// Expand $(config:…) includes per layer, before merge. A static
		// Loader (empty resolver set) performs no include expansion; its
		// tokens fail later, at the resolve step.
		if !l.opts.static() {
			tree, err = l.expandIncludes(tree, dir, []string{filepath.Join(dir, name)})
			if err != nil {
				return nil, fmt.Errorf("flexconf: %w", err)
			}
			if tree.kind != kindMap {
				return nil, fmt.Errorf("flexconf: %s: top-level config must be a map, found a %s", path, tree.kind)
			}
		}
		layers = append(layers, tree)
	}
	return layers, nil
}
