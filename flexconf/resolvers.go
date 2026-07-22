package flexconf

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Resolver turns a token's path into a value for one scheme. Implementations
// are invoked on the merged tree during Load and MAY be called many times per
// Load. A Resolver MUST NOT mutate shared state during Resolve; resolution
// order across tokens is unspecified and resolved values are never re-scanned.
type Resolver interface {
	// Scheme is the token scheme this resolver handles ("env", "secret", ...).
	Scheme() string

	// Resolve maps the path (the text after "scheme:") to a value. The
	// returned string is spliced into the scalar as-is; type conversion
	// happens later at bind time. A resolver MUST NOT include a resolved
	// secret value in its error.
	Resolve(ctx context.Context, path string) (string, error)
}

var (
	globalResolversMu sync.RWMutex
	globalResolvers   = map[string]Resolver{}
)

// defaultSchemes are the schemes New installs on every Loader; they cannot be
// re-registered globally.
var defaultSchemes = map[string]bool{"env": true, "secret": true, "file": true, "config": true}

// RegisterResolver registers a custom-scheme resolver process-wide, so
// importing a package for its side effect makes a scheme available by name.
// Registering a scheme that is already registered (including a default
// scheme: env/secret/file/config) PANICS — schemes are a global namespace and
// silent shadowing is a footgun.
func RegisterResolver(r Resolver) {
	scheme := r.Scheme()
	if !validScheme(scheme) {
		panic(fmt.Sprintf("flexconf: invalid resolver scheme %q", scheme))
	}
	if defaultSchemes[scheme] {
		panic(fmt.Sprintf("flexconf: scheme %q is a default scheme and cannot be re-registered", scheme))
	}
	globalResolversMu.Lock()
	defer globalResolversMu.Unlock()
	if _, dup := globalResolvers[scheme]; dup {
		panic(fmt.Sprintf("flexconf: resolver scheme %q already registered", scheme))
	}
	globalResolvers[scheme] = r
}

// WithResolver overrides or adds a resolver for THIS Loader only (tests, or
// an app that wants a bespoke source without touching the global set). A
// Loader-scoped resolver shadows a global one of the same scheme, and adds to
// / overrides whatever set is in effect (the default set, or one fixed by
// WithResolvers).
func WithResolver(r Resolver) Option {
	return func(o *loaderOptions) {
		if o.scoped == nil {
			o.scoped = map[string]Resolver{}
		}
		o.scoped[r.Scheme()] = r
	}
}

// WithResolvers REPLACES this Loader's default resolver set with exactly the
// resolvers given, instead of the env/secret/file default:
//
//	WithResolvers()              // EMPTY set: a fully static Loader — no token
//	                             // processing, no $(config:) includes at all
//	WithResolvers(myEnv, myFile) // exactly these; no secret: resolver present
//
// WithResolver still composes on top of the resulting set.
func WithResolvers(rs ...Resolver) Option {
	return func(o *loaderOptions) {
		o.replaced = true
		o.set = map[string]Resolver{}
		for _, r := range rs {
			o.set[r.Scheme()] = r
		}
	}
}

// WithEnv overrides the environment source used by the built-in env: resolver
// (testability). The default reads os.LookupEnv.
func WithEnv(env func(string) (string, bool)) Option {
	return func(o *loaderOptions) { o.env = env }
}

// WithFS overrides the file source used by the built-in file: resolver and
// $(config:) includes (testability). Paths are looked up in fsys instead of
// the OS filesystem.
func WithFS(fsys fs.FS) Option {
	return func(o *loaderOptions) { o.fsys = fsys }
}

// static reports whether this Loader is fully static: an explicitly replaced,
// empty resolver set with no loader-scoped additions.
func (o *loaderOptions) static() bool {
	return o.replaced && len(o.set) == 0 && len(o.scoped) == 0
}

// lookupEnv resolves an environment variable through the configured source.
func (l *Loader) lookupEnv(name string) (string, bool) {
	if l.opts.env != nil {
		return l.opts.env(name)
	}
	return os.LookupEnv(name)
}

// readFile reads a file through the configured source (WithFS or the OS).
func (l *Loader) readFile(path string) ([]byte, error) {
	if l.opts.fsys != nil {
		return fs.ReadFile(l.opts.fsys, filepath.ToSlash(path))
	}
	return os.ReadFile(path)
}

// dispatch resolves one token. Built-in schemes (env, file) are handled here
// — file: needs the containing node's directory for relative paths; custom
// schemes go through loader-scoped, replaced-set, then global resolvers.
// The secret: scheme is installed by the default set (see the secret-resolver
// feature); when absent it is an unknown scheme.
func (l *Loader) dispatch(scheme, path string, n *node) (string, error) {
	// Loader-scoped overrides win over everything.
	if r, ok := l.opts.scoped[scheme]; ok {
		return r.Resolve(context.Background(), path)
	}
	if l.opts.replaced {
		if r, ok := l.opts.set[scheme]; ok {
			return r.Resolve(context.Background(), path)
		}
		return "", fmt.Errorf("%w %q", ErrUnknownScheme, scheme)
	}
	// Default set.
	switch scheme {
	case "env":
		value, ok := l.lookupEnv(path)
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrEnvNotSet, path)
		}
		return value, nil
	case "file":
		// Relative paths resolve against the directory of the config file
		// that contains the token, not the process working directory.
		target := path
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(n.file), target)
		}
		data, err := l.readFile(target)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrFileNotFound, path, err)
		}
		return string(data), nil // verbatim: no trailing-newline trimming
	case "secret":
		return l.resolveSecret(path)
	}
	globalResolversMu.RLock()
	r, ok := globalResolvers[scheme]
	globalResolversMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("%w %q", ErrUnknownScheme, scheme)
	}
	return r.Resolve(context.Background(), path)
}
