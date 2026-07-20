package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	flexconf "github.com/sylvanld/go-flexconf"
	"github.com/sylvanld/go-flexconf/settings"
)

type httpConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout int    `yaml:"timeout"`
}

type appConfig struct {
	Name string            `yaml:"name"`
	HTTP flexconf.Settings `yaml:"http"`
}

func defaults() any {
	return &appConfig{
		Name: "example",
		HTTP: flexconf.Defaults(&httpConfig{BaseURL: "https://api.example.com", Timeout: 30}),
	}
}

// newCLI builds the command tree over a temp settings dir and returns it with
// the config path and a buffer capturing its output.
func newCLI(t *testing.T) (*cobra.Command, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := settings.New("example", settings.WithPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	cmd := New(cfg, defaults)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, filepath.Join(dir, "config.yaml"), out
}

func TestInitWritesDefaults(t *testing.T) {
	cmd, file, out := newCLI(t)
	cmd.SetArgs([]string{"init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading written config: %v", err)
	}
	got := string(data)
	for _, want := range []string{"name: example", "base_url: https://api.example.com", "timeout: 30"} {
		if !strings.Contains(got, want) {
			t.Errorf("config missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(out.String(), file) {
		t.Errorf("output should name the written file, got %q", out)
	}
}

// What init writes must be what the loader reads back — otherwise a fresh
// install starts from a file it cannot load.
func TestInitOutputRoundTrips(t *testing.T) {
	cmd, file, out := newCLI(t)
	cmd.SetArgs([]string{"init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	cfg, err := settings.New("example", settings.WithPath(filepath.Dir(file)))
	if err != nil {
		t.Fatal(err)
	}
	// No env injection needed: WithPath outranks EXAMPLE_CONFIG, and the loader
	// reads no environment variable of its own to locate the file.
	var got appConfig
	if err := flexconf.Load(cfg, &got); err != nil {
		t.Fatalf("loading the written config: %v", err)
	}
	if got.Name != "example" {
		t.Errorf("Name = %q, want example", got.Name)
	}
	var h httpConfig
	if err := got.HTTP.Decode(&h); err != nil {
		t.Fatalf("decoding http block: %v", err)
	}
	if h.BaseURL != "https://api.example.com" || h.Timeout != 30 {
		t.Errorf("http = %+v, want {https://api.example.com 30}", h)
	}
}

func TestInitRefusesToClobber(t *testing.T) {
	cmd, file, out := newCLI(t)
	if err := os.WriteFile(file, []byte("name: edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd.SetArgs([]string{"init"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want an already-exists error\n%s", err, out)
	}
	// The edited file must be untouched.
	data, readErr := os.ReadFile(file)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "name: edited\n" {
		t.Errorf("existing config was modified: %q", data)
	}
}

func TestInitForceOverwrites(t *testing.T) {
	cmd, file, out := newCLI(t)
	if err := os.WriteFile(file, []byte("name: edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd.SetArgs([]string{"init", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --force: %v\n%s", err, out)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "base_url:") {
		t.Errorf("config not overwritten:\n%s", data)
	}
}

// init must create the settings directory if it does not exist yet — the
// first-run case it exists to serve.
func TestInitCreatesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cfg")
	cfg, err := settings.New("example", settings.WithPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	cmd := New(cfg, defaults)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("config not written: %v", err)
	}
}

func TestPathPrintsConfigFile(t *testing.T) {
	cmd, file, out := newCLI(t)
	cmd.SetArgs([]string{"path"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("path: %v", err)
	}
	if strings.TrimSpace(out.String()) != file {
		t.Errorf("got %q, want %q", strings.TrimSpace(out.String()), file)
	}
}
