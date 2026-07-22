package flexconf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
	_ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass"
	"github.com/sylvanld/go-flexconf/internal/agent"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// TestMain wires the agent self-exec entry point so PolicyAgent tests can
// spawn agents from the test binary.
func TestMain(m *testing.M) {
	RunAgentIfRequested()
	os.Exit(m.Run())
}

// setupSecretVault creates a KeePass vault with secrets and a registry file
// naming it as the default vault.
func setupSecretVault(t *testing.T, secrets map[string]string) {
	t.Helper()
	dir := t.TempDir()
	kdbx := filepath.Join(dir, "vault.kdbx")

	drv, err := flexvault.New("keepass")
	if err != nil {
		t.Fatal(err)
	}
	mgr := flexvault.NewManager(drv,
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"})))
	decode := flexvault.MapDecoder(map[string]any{"path": kdbx})
	if err := mgr.Create(context.Background(), decode); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Open(context.Background(), decode); err != nil {
		t.Fatal(err)
	}
	for addr, value := range secrets {
		if err := mgr.Set(context.Background(), addr, value); err != nil {
			t.Fatal(err)
		}
	}
	mgr.Lock()

	regFile := filepath.Join(dir, "vaults.yaml")
	content := fmt.Sprintf("default: main\nvaults:\n  main:\n    driver: keepass\n    path: %s\n", kdbx)
	if err := os.WriteFile(regFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(vaultreg.EnvVaults, regFile)
	t.Setenv("FLEXCONF_RUNTIME_DIR", filepath.Join(dir, "run"))
	t.Setenv("FLEXCONF_IDLE_TIMEOUT", "30s")
	t.Cleanup(func() { flexprompt.SetPrompter(nil) })
}

type secretConfig struct {
	Service string `flexconf:"service"`
	Token   string `flexconf:"token"`
	URL     string `flexconf:"url"`
}

func TestSecretResolverInProcess(t *testing.T) {
	setupSecretVault(t, map[string]string{
		"artifactory/token": "tok-99",
		"net/host":          "vault.example.com",
	})
	flexprompt.SetPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"}))

	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
service: api
token: $(secret:artifactory/token)
url: https://$(secret:net/host)/api
`)
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()

	var cfg secretConfig
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "tok-99" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if cfg.URL != "https://vault.example.com/api" {
		t.Fatalf("URL = %q (secret embeddable in text)", cfg.URL)
	}
}

func TestSecretQualifiedVault(t *testing.T) {
	setupSecretVault(t, map[string]string{"deploy/key": "k-1"})
	flexprompt.SetPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"}))

	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "token: $(secret:main:deploy/key)\nservice: s\nurl: u\n")
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()
	var cfg secretConfig
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "k-1" {
		t.Fatalf("Token = %q", cfg.Token)
	}
}

func TestSecretUnknownVault(t *testing.T) {
	setupSecretVault(t, nil)
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "token: $(secret:nope:a/b)\nservice: s\nurl: u\n")
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()
	var cfg secretConfig
	err := l.Load("config.yaml", &cfg)
	if err == nil || !stringsContains(err.Error(), `unknown vault "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretMissingIsLoud(t *testing.T) {
	setupSecretVault(t, nil)
	flexprompt.SetPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"}))
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "token: $(secret:missing/key)\nservice: s\nurl: u\n")
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()
	var cfg secretConfig
	err := l.Load("config.yaml", &cfg)
	if !errors.Is(err, flexvault.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !stringsContains(err.Error(), "token") {
		t.Fatalf("err %q should name the key path", err)
	}
}

func TestSecretPromptsOncePerVault(t *testing.T) {
	setupSecretVault(t, map[string]string{"a/x": "1", "b/y": "2", "c/z": "3"})
	prompts := 0
	flexprompt.SetPrompter(flexprompt.PrompterFunc(func(_ context.Context, reqs []flexprompt.PromptRequest) (map[string]string, error) {
		prompts++
		return map[string]string{"password": "master"}, nil
	}))

	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
service: $(secret:a/x)
token: $(secret:b/y)
url: $(secret:c/z)
`)
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()
	var cfg secretConfig
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if prompts != 1 {
		t.Fatalf("prompts = %d, want 1 (once per referenced vault)", prompts)
	}
	// A second Load on the same Loader reuses the unlocked Manager.
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if prompts != 1 {
		t.Fatalf("prompts after re-Load = %d, want 1", prompts)
	}
}

func TestSecretRedactionEndToEnd(t *testing.T) {
	setupSecretVault(t, map[string]string{"db/port": "not-a-port-and-secret"})
	flexprompt.SetPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"}))
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "port: $(secret:db/port)\n")
	var cfg struct {
		Port int `flexconf:"port"`
	}
	l := New(dir).With(WithSecretPolicy(PolicyInProcess))
	defer l.Close()
	err := l.Load("config.yaml", &cfg)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
	if stringsContains(err.Error(), "not-a-port-and-secret") {
		t.Fatalf("err %q leaks the secret value", err)
	}
}

func TestSecretPolicyAgent(t *testing.T) {
	setupSecretVault(t, map[string]string{"artifactory/token": "agent-tok"})
	flexprompt.SetPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "master"}))

	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "token: $(secret:artifactory/token)\nservice: s\nurl: u\n")
	l := New(dir) // PolicyAgent is the default
	defer l.Close()
	var cfg secretConfig
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "agent-tok" {
		t.Fatalf("Token = %q", cfg.Token)
	}

	// The agent stays resident: a FRESH Loader with a prompter that fails on
	// any request still resolves (no re-prompt — the running agent serves it).
	flexprompt.SetPrompter(flexprompt.PrompterFunc(func(_ context.Context, reqs []flexprompt.PromptRequest) (map[string]string, error) {
		if len(reqs) > 0 {
			t.Error("must not prompt while the agent holds the vault")
		}
		return map[string]string{}, nil
	}))
	l2 := New(dir)
	defer l2.Close()
	var cfg2 secretConfig
	if err := l2.Load("config.yaml", &cfg2); err != nil {
		t.Fatalf("Load via running agent: %v", err)
	}
	if cfg2.Token != "agent-tok" {
		t.Fatalf("Token = %q", cfg2.Token)
	}

	// Shut the agent down.
	reg, _ := vaultreg.Load()
	name, conf, _ := reg.Resolve("")
	if c, err := agent.Dial(vaultreg.VaultID(name, conf)); err == nil {
		c.Lock()
		c.Close()
	}
}
