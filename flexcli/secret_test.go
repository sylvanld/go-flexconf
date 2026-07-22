package flexcli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
	_ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

func TestMain(m *testing.M) {
	New(Options{}).RunAgentIfRequested()
	os.Exit(m.Run())
}

// setupRegistry writes a registry naming one keepass vault (not yet created)
// and scopes runtime/agent env to the test.
func setupRegistry(t *testing.T, name string) (kdbxPath string) {
	t.Helper()
	dir := t.TempDir()
	kdbxPath = filepath.Join(dir, name+".kdbx")
	regFile := filepath.Join(dir, "vaults.yaml")
	content := fmt.Sprintf("default: %s\nvaults:\n  %s:\n    driver: keepass\n    path: %s\n", name, name, kdbxPath)
	if err := os.WriteFile(regFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(vaultreg.EnvVaults, regFile)
	t.Setenv("FLEXCONF_RUNTIME_DIR", filepath.Join(dir, "run"))
	t.Setenv("FLEXCONF_IDLE_TIMEOUT", "30s")
	return kdbxPath
}

// run executes the secret group with args and returns stdout and the error.
func run(t *testing.T, app *App, stdin string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(app.SecretCommand())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func testApp(password string) *App {
	return New(Options{Prompter: flexprompt.NewMapPrompter(map[string]string{"password": password})})
}

func lockAgent(t *testing.T, app *App) {
	t.Helper()
	run(t, app, "", "secret", "lock")
}

func TestInitUnlockGetSetListLock(t *testing.T) {
	setupRegistry(t, "personal")
	app := testApp("hunter2")
	t.Cleanup(func() { lockAgent(t, app) })

	out, err := run(t, app, "", "secret", "init")
	if err != nil || !strings.Contains(out, `created vault "personal"`) {
		t.Fatalf("init: %q, %v", out, err)
	}

	// init never clobbers.
	if _, err := run(t, app, "", "secret", "init"); err == nil {
		t.Fatal("second init must fail")
	}

	out, err = run(t, app, "", "secret", "unlock")
	if err != nil || !strings.Contains(out, "unlocked") {
		t.Fatalf("unlock: %q, %v", out, err)
	}

	// Re-unlock is a no-op success.
	out, err = run(t, app, "", "secret", "unlock")
	if err != nil || !strings.Contains(out, "already unlocked") {
		t.Fatalf("re-unlock: %q, %v", out, err)
	}

	// set reads the value from stdin.
	out, err = run(t, app, "tok-abc", "secret", "set", "artifactory/token")
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("set: %q, %v", out, err)
	}

	out, err = run(t, app, "", "secret", "get", "artifactory/token")
	if err != nil || out != "tok-abc\n" {
		t.Fatalf("get: %q, %v", out, err)
	}

	// --raw emits the exact value, no trailing newline.
	out, err = run(t, app, "", "secret", "get", "artifactory/token", "--raw")
	if err != nil || out != "tok-abc" {
		t.Fatalf("get --raw: %q, %v", out, err)
	}

	out, err = run(t, app, "", "secret", "list")
	if err != nil || !strings.Contains(out, "artifactory") {
		t.Fatalf("list: %q, %v", out, err)
	}
	out, err = run(t, app, "", "secret", "list", "artifactory")
	if err != nil || !strings.Contains(out, "token") {
		t.Fatalf("list ns: %q, %v", out, err)
	}

	out, err = run(t, app, "", "secret", "status")
	if err != nil || !strings.Contains(out, "unlocked") {
		t.Fatalf("status: %q, %v", out, err)
	}

	out, err = run(t, app, "", "secret", "lock")
	if err != nil || !strings.Contains(out, "locked; agent stopped") {
		t.Fatalf("lock: %q, %v", out, err)
	}
	out, err = run(t, app, "", "secret", "lock")
	if err != nil || !strings.Contains(out, "no agent running") {
		t.Fatalf("lock idempotent: %q, %v", out, err)
	}
}

func TestGetLockedNoUnlockFails(t *testing.T) {
	setupRegistry(t, "locked")
	app := testApp("pw")
	if _, err := run(t, app, "", "secret", "init"); err != nil {
		t.Fatal(err)
	}
	// Non-interactive (test stdin is not a TTY): fails with locked guidance.
	_, err := run(t, app, "", "secret", "get", "a/b", "--no-unlock")
	if !errors.Is(err, flexvault.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if ExitCode(err) != ExitLocked {
		t.Fatalf("ExitCode = %d, want %d", ExitCode(err), ExitLocked)
	}
}

func TestGetWithAutoUnlockFlag(t *testing.T) {
	setupRegistry(t, "auto")
	app := testApp("pw")
	t.Cleanup(func() { lockAgent(t, app) })
	if _, err := run(t, app, "", "secret", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, app, "v", "secret", "set", "ns/k", "--unlock"); err != nil {
		t.Fatalf("set --unlock: %v", err)
	}
	out, err := run(t, app, "", "secret", "get", "ns/k")
	if err != nil || out != "v\n" {
		t.Fatalf("get: %q, %v", out, err)
	}
}

func TestBadAddressIsUsageError(t *testing.T) {
	setupRegistry(t, "addr")
	app := testApp("pw")
	if _, err := run(t, app, "", "secret", "get", "not-an-address"); err == nil {
		t.Fatal("invalid address must fail before contacting any agent")
	}
}

func TestUnknownVaultFlag(t *testing.T) {
	setupRegistry(t, "kv")
	app := testApp("pw")
	_, err := run(t, app, "", "secret", "--vault", "nope", "status")
	if err == nil || !strings.Contains(err.Error(), `unknown vault "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestVaultsCommand(t *testing.T) {
	setupRegistry(t, "shown")
	app := testApp("pw")

	out, err := run(t, app, "", "secret", "vaults")
	if err != nil {
		t.Fatalf("vaults: %v", err)
	}
	for _, want := range []string{"registry files (in order):", "[ok]", "default: shown", "driver=keepass"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}

	// YAML dump round-trips as a plain registry document.
	out, err = run(t, app, "", "secret", "vaults", "--format", "yaml")
	if err != nil || !strings.Contains(out, "driver: keepass") || strings.Contains(out, "[ok]") {
		t.Fatalf("vaults --format yaml: %q, %v", out, err)
	}
}

func TestVaultsValidate(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "vaults.yaml")
	// default names a missing vault; driver is unregistered.
	os.WriteFile(regFile, []byte("default: ghost\nvaults:\n  v:\n    driver: nodriver\n    path: /x\n"), 0o600)
	t.Setenv(vaultreg.EnvVaults, regFile)
	app := testApp("pw")

	// Dump-only: informational, exits 0.
	out, err := run(t, app, "", "secret", "vaults")
	if err != nil || !strings.Contains(out, "note:") {
		t.Fatalf("vaults (dump-only): %q, %v", out, err)
	}
	// --validate: the same problems become an error.
	if _, err := run(t, app, "", "secret", "vaults", "--validate"); err == nil {
		t.Fatal("vaults --validate should fail on a broken registry")
	}
}

func TestSecretsAlias(t *testing.T) {
	setupRegistry(t, "alias")
	app := testApp("pw")
	if _, err := run(t, app, "", "secrets", "vaults"); err != nil {
		t.Fatalf("secrets alias: %v", err)
	}
}
