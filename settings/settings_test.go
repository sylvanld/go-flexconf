package settings

import (
	"path/filepath"
	"testing"
)

// mapEnv is a fixed Env for tests.
type mapEnv map[string]string

func (m mapEnv) Lookup(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

func TestNewDefaultsToUserConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	// Empty reads as unset, so an ambient FLEXCONF_CONFIG cannot steer this.
	t.Setenv("FLEXCONF_CONFIG", "")

	s, err := New("flexconf")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := "/tmp/xdg-config/flexconf"
	if s.Path() != want {
		t.Fatalf("Path() = %q, want %q", s.Path(), want)
	}
	if !s.IsDefault() {
		t.Fatal("IsDefault() = false, want true")
	}
	if got := s.File("store.kdbx"); got != filepath.Join(want, "store.kdbx") {
		t.Fatalf("File() = %q", got)
	}
}

func TestWithPathOverride(t *testing.T) {
	s, err := New("flexconf", WithPath("/etc/flexconf"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Path() != "/etc/flexconf" {
		t.Fatalf("Path() = %q, want /etc/flexconf", s.Path())
	}
	if s.IsDefault() {
		t.Fatal("IsDefault() = true, want false for overridden path")
	}
}

func TestEmptyAppName(t *testing.T) {
	if _, err := New(""); err != ErrEmptyAppName {
		t.Fatalf("New(\"\") err = %v, want ErrEmptyAppName", err)
	}
}

func TestEnsureDirCreates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "flexconf")
	s, err := New("flexconf", WithPath(dir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir (idempotent): %v", err)
	}
}

// <APP>_CONFIG names the config directory, and every path derived from the
// AppConfig moves with it as a unit — that is the whole point of resolving the
// directory in one place.
func TestEnvVarOverridesDirectory(t *testing.T) {
	s, err := New("flexconf", WithEnv(mapEnv{"FLEXCONF_CONFIG": "/scratch/cfg"}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Path() != "/scratch/cfg" {
		t.Errorf("Path() = %q, want /scratch/cfg", s.Path())
	}
	if got, want := s.File("config.yaml"), "/scratch/cfg/config.yaml"; got != want {
		t.Errorf("File(config.yaml) = %q, want %q", got, want)
	}
	if got, want := s.File("secrets.kdbx"), "/scratch/cfg/secrets.kdbx"; got != want {
		t.Errorf("File(secrets.kdbx) = %q, want %q — the whole dir must move together", got, want)
	}
	if s.IsDefault() {
		t.Error("IsDefault() = true, want false for an env override")
	}
}

func TestWithPathOutranksEnvVar(t *testing.T) {
	s, err := New("flexconf",
		WithPath("/explicit"),
		WithEnv(mapEnv{"FLEXCONF_CONFIG": "/from-env"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Path() != "/explicit" {
		t.Errorf("Path() = %q, want /explicit", s.Path())
	}
}

// An empty variable reads as unset, so exporting it blank never clobbers the
// default — the same discipline WithPath("") follows.
func TestEmptyEnvVarFallsThroughToDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

	s, err := New("flexconf", WithEnv(mapEnv{"FLEXCONF_CONFIG": ""}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := "/tmp/xdg-config/flexconf"; s.Path() != want {
		t.Errorf("Path() = %q, want %q", s.Path(), want)
	}
}

func TestEnvVarNaming(t *testing.T) {
	for _, tc := range []struct{ app, want string }{
		{"myapp", "MYAPP_CONFIG"},
		{"my-app", "MY_APP_CONFIG"},
		{"my.app-2", "MY_APP_2_CONFIG"},
	} {
		if got := EnvVar(tc.app); got != tc.want {
			t.Errorf("EnvVar(%q) = %q, want %q", tc.app, got, tc.want)
		}
	}
}
