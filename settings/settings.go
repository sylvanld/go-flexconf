package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrEmptyAppName is returned when an AppConfig is built without an app name.
var ErrEmptyAppName = errors.New("settings: app name must not be empty")

// Env resolves environment variables. Injected so tests run against a fixed map
// rather than the real process environment. It is declared here, rather than
// reused from the loader, because the loader imports this package — sharing the
// type would invert that dependency. The method set is identical, so a
// loader.OSEnv or loader.MapEnv satisfies it directly.
type Env interface {
	Lookup(name string) (value string, ok bool)
}

// OSEnv is the production Env: the process environment.
type OSEnv struct{}

// Lookup reports the process environment variable named name.
func (OSEnv) Lookup(name string) (string, bool) { return os.LookupEnv(name) }

// AppConfig describes where an application's configuration lives.
//
// The settings directory is resolved once, here, and every path the application
// derives hangs off it — the config file, the KeePass store, anything else via
// File. That single resolution point is deliberate: an override that moved only
// some of those would leave an app reading its config from one directory and its
// secrets from another.
//
// Precedence: WithPath, then the <APP>_CONFIG environment variable (see EnvVar),
// then DefaultPath(appName) — <user-config-dir>/<app_name>, i.e.
// ~/.config/<app_name>/ on Linux, honoring XDG_CONFIG_HOME.
//
// <APP>_CONFIG names the config *directory*, never a file. The main config file
// is always config.yaml inside it, so the variable relocates the whole
// directory as a unit.
type AppConfig struct {
	appName string
	path    string
}

// options collects what New resolves the path from. Options are applied before
// resolution so an explicit WithPath outranks the environment.
type options struct {
	path string
	env  Env
}

// Option configures an AppConfig during New.
type Option func(*options)

// WithPath overrides the settings directory, outranking <APP>_CONFIG. An empty
// path is ignored, so an unset override never clobbers the resolution below it.
func WithPath(path string) Option {
	return func(o *options) {
		if path != "" {
			o.path = path
		}
	}
}

// WithEnv injects the environment the <APP>_CONFIG lookup reads. Defaults to the
// process environment. A nil env is ignored.
func WithEnv(env Env) Option {
	return func(o *options) {
		if env != nil {
			o.env = env
		}
	}
}

// EnvVar returns the environment variable naming appName's config directory:
// the uppercased app name with every non-alphanumeric character replaced by an
// underscore, suffixed with _CONFIG. For "my-app" that is MY_APP_CONFIG.
func EnvVar(appName string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(appName) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name := b.String()
	if name == "" {
		name = "APP"
	}
	return name + "_CONFIG"
}

// New builds an AppConfig for appName, resolving the settings directory by the
// precedence documented on AppConfig: WithPath, then <APP>_CONFIG, then
// DefaultPath(appName).
func New(appName string, opts ...Option) (*AppConfig, error) {
	if appName == "" {
		return nil, ErrEmptyAppName
	}

	o := options{env: OSEnv{}}
	for _, opt := range opts {
		opt(&o)
	}

	path := o.path
	if path == "" {
		if v, ok := o.env.Lookup(EnvVar(appName)); ok && v != "" {
			path = v
		}
	}
	if path == "" {
		def, err := DefaultPath(appName)
		if err != nil {
			return nil, err
		}
		path = def
	}
	return &AppConfig{appName: appName, path: path}, nil
}

// DefaultPath returns the default settings directory for appName:
// <user-config-dir>/<app_name> (~/.config/<app_name>/ on Linux, honoring
// XDG_CONFIG_HOME).
func DefaultPath(appName string) (string, error) {
	if appName == "" {
		return "", ErrEmptyAppName
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

// AppName returns the application name.
func (s *AppConfig) AppName() string { return s.appName }

// Path returns the resolved settings directory.
func (s *AppConfig) Path() string { return s.path }

// DefaultPath returns the default settings directory for this app, regardless of
// any override applied via WithPath.
func (s *AppConfig) DefaultPath() (string, error) {
	return DefaultPath(s.appName)
}

// IsDefault reports whether the resolved path is the default one.
func (s *AppConfig) IsDefault() bool {
	def, err := DefaultPath(s.appName)
	return err == nil && def == s.path
}

// File returns the path to name inside the settings directory.
func (s *AppConfig) File(name string) string {
	return filepath.Join(s.path, name)
}

// EnsureDir creates the settings directory (and parents) if it does not exist.
func (s *AppConfig) EnsureDir() error {
	return os.MkdirAll(s.path, 0o755)
}
