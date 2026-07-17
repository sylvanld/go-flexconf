package settings

import (
	"path/filepath"
	"testing"
)

func TestNewDefaultsToUserConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

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
