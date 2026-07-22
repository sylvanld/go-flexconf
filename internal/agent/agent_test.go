package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
	_ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// TestMain wires the self-exec entry point: when the test binary is
// re-executed as an agent, it runs the agent loop instead of the tests.
func TestMain(m *testing.M) {
	RunAgentIfRequested()
	os.Exit(m.Run())
}

// setupVault creates a KeePass vault and a registry describing it, and points
// the runtime dir and FLEXCONF_VAULTS at test-scoped locations.
func setupVault(t *testing.T, name, password string) (vaultID string) {
	t.Helper()
	dir := t.TempDir()
	kdbx := filepath.Join(dir, name+".kdbx")

	// Create the vault with a secret in it.
	drv, err := flexvault.New("keepass")
	if err != nil {
		t.Fatal(err)
	}
	mgr := flexvault.NewManager(drv,
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": password})))
	decode := flexvault.MapDecoder(map[string]any{"path": kdbx})
	if err := mgr.Create(context.Background(), decode); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Open(context.Background(), decode); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Set(context.Background(), "artifactory/token", "tok-42"); err != nil {
		t.Fatal(err)
	}
	mgr.Lock()

	// Registry file naming the vault.
	regFile := filepath.Join(dir, "vaults.yaml")
	content := fmt.Sprintf("default: %s\nvaults:\n  %s:\n    driver: keepass\n    path: %s\n", name, name, kdbx)
	if err := os.WriteFile(regFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(vaultreg.EnvVaults, regFile)
	t.Setenv(envRuntime, filepath.Join(dir, "run"))
	t.Setenv(envIdle, "30s")

	reg, err := vaultreg.Load()
	if err != nil {
		t.Fatal(err)
	}
	rn, conf, err := reg.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	return vaultreg.VaultID(rn, conf)
}

func TestSpawnUnlockGetLock(t *testing.T) {
	vaultID := setupVault(t, "work", "master")

	if Running(vaultID) {
		t.Fatal("no agent should be running yet")
	}
	if err := Spawn(vaultID, "work"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		if c, err := Dial(vaultID); err == nil {
			c.Lock()
			c.Close()
		}
	})

	client, err := Dial(vaultID)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// Locked agent rejects get with ErrLocked.
	if _, err := client.Get("artifactory/token"); !errors.Is(err, flexvault.ErrLocked) {
		t.Fatalf("Get before unlock err = %v, want ErrLocked", err)
	}

	// Bad credentials map back to ErrAuth.
	if err := client.Unlock(map[string]string{"password": "wrong"}); !errors.Is(err, flexvault.ErrAuth) {
		t.Fatalf("Unlock(wrong) err = %v, want ErrAuth", err)
	}

	if err := client.Unlock(map[string]string{"password": "master"}); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	v, err := client.Get("artifactory/token")
	if err != nil || v != "tok-42" {
		t.Fatalf("Get = %q, %v", v, err)
	}

	// A second client shares the unlocked agent (no re-unlock).
	c2, err := Dial(vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c2.Get("artifactory/token"); err != nil || v != "tok-42" {
		t.Fatalf("second client Get = %q, %v", v, err)
	}

	// Set through the agent persists.
	if err := c2.Set("db/pw", "s3cret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, _ := client.Get("db/pw"); v != "s3cret" {
		t.Fatalf("Get after Set = %q", v)
	}
	names, err := client.List("")
	if err != nil || len(names) != 2 {
		t.Fatalf("List = %v, %v", names, err)
	}
	c2.Close()

	// Status reports unlocked and does not error.
	st, err := client.Status()
	if err != nil || !st.Unlocked || st.VaultID != vaultID || !st.Writable {
		t.Fatalf("Status = %+v, %v", st, err)
	}

	// Missing secret maps to ErrNotFound.
	if _, err := client.Get("nope/missing"); !errors.Is(err, flexvault.ErrNotFound) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}

	// Graceful lock shuts the agent down and removes the socket.
	if err := client.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for Running(vaultID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if Running(vaultID) {
		t.Fatal("agent should exit after lock")
	}
}

func TestSpawnRaceIsSafe(t *testing.T) {
	vaultID := setupVault(t, "race", "pw")
	t.Cleanup(func() {
		if c, err := Dial(vaultID); err == nil {
			c.Lock()
			c.Close()
		}
	})
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { done <- Spawn(vaultID, "race") }()
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatalf("racing Spawn: %v", err)
		}
	}
	if !Running(vaultID) {
		t.Fatal("an agent should be running")
	}
}

func TestProxyDriver(t *testing.T) {
	setupVault(t, "proxied", "pw")
	MarkEntryPointWired()

	proxy, err := NewProxy("proxied")
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() {
		if c, err := Dial(proxy.VaultID()); err == nil {
			c.Lock()
			c.Close()
		}
	})

	// Drive the proxy through a Manager, like the secret: resolver does.
	mgr := flexvault.NewManager(proxy,
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "pw"})))
	if err := mgr.Open(context.Background(), flexvault.MapDecoder(nil)); err != nil {
		t.Fatalf("Open (spawn+unlock through proxy): %v", err)
	}
	v, err := mgr.Get(context.Background(), "artifactory/token")
	if err != nil || v != "tok-42" {
		t.Fatalf("Get = %q, %v", v, err)
	}

	// A second proxy reuses the running agent without prompting.
	proxy2, err := NewProxy("proxied")
	if err != nil {
		t.Fatal(err)
	}
	failing := flexprompt.PrompterFunc(func(_ context.Context, reqs []flexprompt.PromptRequest) (map[string]string, error) {
		if len(reqs) > 0 {
			t.Error("must not prompt when the agent is already unlocked")
		}
		return map[string]string{}, nil
	})
	mgr2 := flexvault.NewManager(proxy2, flexvault.WithPrompter(failing))
	if err := mgr2.Open(context.Background(), flexvault.MapDecoder(nil)); err != nil {
		t.Fatalf("Open via running agent: %v", err)
	}
	if v, err := mgr2.Get(context.Background(), "artifactory/token"); err != nil || v != "tok-42" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}

func TestIdleAutoLock(t *testing.T) {
	vaultID := setupVault(t, "idle", "pw")
	t.Setenv(envIdle, "500ms")
	if err := Spawn(vaultID, "idle"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	client, err := Dial(vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Unlock(map[string]string{"password": "pw"}); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	client.Close()

	deadline := time.Now().Add(5 * time.Second)
	for Running(vaultID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Running(vaultID) {
		t.Fatal("agent should auto-lock and exit after the idle timeout")
	}
}
