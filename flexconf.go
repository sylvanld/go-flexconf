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
// github.com/sylvanld/go-flexconf.
package flexconf

import (
	"github.com/sylvanld/go-flexconf/internal/loader"
	"github.com/sylvanld/go-flexconf/settings"
)

// --- App context -----------------------------------------------------------

// AppConfig describes an application's identity and where its config lives (the
// app name and its config directory). It is the context handed to Load — not
// the loaded config content, which is a Settings.
type AppConfig = settings.AppConfig

// NewAppConfig builds an AppConfig for appName. The config directory defaults to
// the platform's per-user config dir (~/.config/<app>/ on Linux, honoring
// XDG_CONFIG_HOME) and can be overridden with WithAppPath.
var NewAppConfig = settings.New

// WithAppPath overrides an AppConfig's config directory, outranking <APP>_CONFIG.
var WithAppPath = settings.WithPath

// WithAppEnv injects the environment the <APP>_CONFIG directory lookup reads.
var WithAppEnv = settings.WithEnv

// AppEnvVar returns the environment variable naming an app's config *directory*
// — e.g. MYAPP_CONFIG for "myapp". It never names a file: the main config file
// is always ConfigFileName inside that directory.
var AppEnvVar = settings.EnvVar

// ConfigFileName is the main config file, always this name relative to the
// settings directory.
const ConfigFileName = loader.ConfigFileName

// --- Loading ---------------------------------------------------------------

// Settings is lazily-loaded config: a block located, templated, and
// secret-resolved, but not yet decoded into a typed struct. LoadFile returns one
// for a whole config file; it is also usable as a struct field, so a parent can
// capture a child block without knowing its shape and let the owning subsystem
// Decode it later. Dump renders it back to YAML with secrets redacted. See
// PolymorphicSettings for blocks whose type is chosen by a discriminator field.
//
// A Settings may carry a default (see Defaults) that a loaded block decodes
// over, so an omitted or partial block still yields fully-populated settings.
type Settings = loader.Settings

// Defaults builds a Settings whose fallback tree is v — a pre-populated Go value
// — marshalled to YAML. Assign it to a field of a default config struct so the
// block has a shape even when the config file omits it; loading decodes the
// file's keys on top, per key. Marshalling that struct renders the defaults,
// which is what the `settings init` command writes.
var Defaults = loader.Defaults

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

// --- Polymorphic settings --------------------------------------------------

// PolymorphicSettings resolves a lazily-loaded Settings block into one of
// several concrete types, chosen at decode time by a discriminator field in the
// data — a config block whose shape depends on one of its own fields. I is the
// interface every variant satisfies. Build one with NewPolymorphicSettings.
type PolymorphicSettings[I any] = loader.PolymorphicSettings[I]

// NewPolymorphicSettings builds a variant registry keyed by the discriminator
// field (e.g. "type", or a domain-specific "engine"/"channel"). There is no
// default — each block names its own selector. Register the variants, then
// Decode a captured Settings into the concrete type its discriminator selects.
//
// A variant's factory returning a pre-populated value declares that variant's
// defaults: Decode decodes the block over it, so the block overrides only the
// keys it names. SetDefault additionally names the variant a block omitting the
// discriminator resolves to, and DefaultSettings renders that variant as a
// complete block for `settings init`.
func NewPolymorphicSettings[I any](discriminator string) *PolymorphicSettings[I] {
	return loader.NewPolymorphicSettings[I](discriminator)
}

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
