// Package flexconf is the flexible configuration loader of
// docs/specs/settings.md: it turns a configuration file into the typed struct
// an application decodes into. Owned steps are locate → read → parse → template
// → (decode). Templating resolves $(env:…), $(secret:…) and $(config:…)
// references on the parsed node tree (never on raw text), tracking
// secret-sourced nodes as tainted so every config dump can redact them.
// $(secret:…) resolves through the toolkit's existing secrets.Store; which
// driver backs it is chosen by an injected store, a `secrets` block, or the
// zero-config agent→keepass default.
//
// This file is a thin façade: the implementation lives in internal/loader and
// is re-exported here so callers import a single, flat public surface as
// github.com/sylvanld/flexconf.
package flexconf

import "github.com/sylvanld/flexconf/internal/loader"

// --- Loading ---------------------------------------------------------------

// Loaded is the result of the template pass: the substituted node tree and the
// taint set of secret-sourced scalars. Decode unmarshals it into a typed
// struct; Dump renders it back to YAML with secrets redacted.
type Loaded = loader.Loaded

// Option customises a Load.
type Option = loader.Option

// Load resolves the config file, reads/parses/templates it, and unmarshals the
// result into out. It is LoadFile followed by Decode.
var Load = loader.Load

// LoadFile runs the locate→read→parse→template pipeline and returns the
// templated tree plus its taint set, without decoding.
var LoadFile = loader.LoadFile

var (
	// WithConfigFile sets an explicit config file path (highest precedence).
	WithConfigFile = loader.WithConfigFile
	// WithEnv injects the environment used for $(env:…) and the <APP>_CONFIG lookup.
	WithEnv = loader.WithEnv
	// WithSecretStore injects a ready *secrets.Store as the $(secret:…) resolver.
	WithSecretStore = loader.WithSecretStore
	// WithSecretResolver injects a SecretResolver directly.
	WithSecretResolver = loader.WithSecretResolver
	// WithFS reads the root config and its includes from an fs.FS.
	WithFS = loader.WithFS
)

// Redacted is what a secret-sourced scalar renders as in any config dump.
const Redacted = loader.Redacted

// NodeSet marks nodes of a templated tree, used for the secret taint set.
type NodeSet = loader.NodeSet

// --- Secrets ---------------------------------------------------------------

// SecretResolver resolves a $(secret:NAME) reference to its value.
type SecretResolver = loader.SecretResolver

// SecretDriverFactory builds a secrets.Driver from its config sub-block.
type SecretDriverFactory = loader.SecretDriverFactory

// StoreResolver adapts a *secrets.Store to a SecretResolver.
var StoreResolver = loader.StoreResolver

// RegisterSecretDriver registers a driver factory under name, panicking on a
// duplicate. An unknown name in a config is a fatal load error.
var RegisterSecretDriver = loader.RegisterSecretDriver

// --- Environment -----------------------------------------------------------

// Env resolves environment variables.
type Env = loader.Env

// OSEnv is the production Env: the process environment.
type OSEnv = loader.OSEnv

// MapEnv is a fixed-map Env for tests.
type MapEnv = loader.MapEnv
