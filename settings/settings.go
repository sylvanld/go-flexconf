package settings

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrEmptyAppName is returned when a Settings is built without an app name.
var ErrEmptyAppName = errors.New("settings: app name must not be empty")

// Settings describes where an application's configuration lives.
//
// Unless a path is supplied via WithPath, the settings directory defaults to
// DefaultPath(appName), i.e. <user-config-dir>/<app_name> — ~/.config/<app_name>/
// on Linux, honoring XDG_CONFIG_HOME.
type Settings struct {
	appName string
	path    string
}

// Option configures a Settings during New.
type Option func(*Settings)

// WithPath overrides the settings path. An empty path is ignored, leaving the
// default in place.
func WithPath(path string) Option {
	return func(s *Settings) {
		if path != "" {
			s.path = path
		}
	}
}

// New builds Settings for appName. The settings path defaults to
// DefaultPath(appName) and can be overridden with WithPath.
func New(appName string, opts ...Option) (*Settings, error) {
	if appName == "" {
		return nil, ErrEmptyAppName
	}

	def, err := DefaultPath(appName)
	if err != nil {
		return nil, err
	}

	s := &Settings{appName: appName, path: def}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
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
func (s *Settings) AppName() string { return s.appName }

// Path returns the resolved settings directory.
func (s *Settings) Path() string { return s.path }

// DefaultPath returns the default settings directory for this app, regardless of
// any override applied via WithPath.
func (s *Settings) DefaultPath() (string, error) {
	return DefaultPath(s.appName)
}

// IsDefault reports whether the resolved path is the default one.
func (s *Settings) IsDefault() bool {
	def, err := DefaultPath(s.appName)
	return err == nil && def == s.path
}

// File returns the path to name inside the settings directory.
func (s *Settings) File(name string) string {
	return filepath.Join(s.path, name)
}

// EnsureDir creates the settings directory (and parents) if it does not exist.
func (s *Settings) EnsureDir() error {
	return os.MkdirAll(s.path, 0o755)
}
