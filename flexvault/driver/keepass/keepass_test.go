package keepass

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sylvanld/go-flexconf/flexprompt"
	"github.com/sylvanld/go-flexconf/flexvault"
)

// newVault creates a fresh .kdbx via the Manager (Create) and returns an
// unlocked Manager over it plus its path.
func newVault(t *testing.T, password string) (*flexvault.Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.kdbx")
	m := flexvault.NewManager(New(),
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{CredPassword: password})))
	decode := flexvault.MapDecoder(map[string]any{"path": path})
	if err := m.Create(context.Background(), decode); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Open(context.Background(), decode); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Lock() })
	return m, path
}

func TestRegisteredByName(t *testing.T) {
	d, err := flexvault.New("keepass")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.Name() != "keepass" {
		t.Fatalf("Name = %q", d.Name())
	}
}

func TestConfigureRequiresPath(t *testing.T) {
	d := New()
	if err := d.Configure(flexvault.MapDecoder(map[string]any{})); err == nil {
		t.Fatal("Configure without path should fail")
	}
	if caps := d.Capabilities(); caps != (flexvault.Capabilities{}) {
		t.Fatalf("Capabilities before Configure = %+v, want zero", caps)
	}
}

func TestCreateSetGetList(t *testing.T) {
	ctx := context.Background()
	m, _ := newVault(t, "master")

	if err := m.Set(ctx, "artifactory/token", "tok-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Set(ctx, "artifactory/user", "alice"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Set(ctx, "database/password", "db-pw"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	v, err := m.Get(ctx, "artifactory/token")
	if err != nil || v != "tok-123" {
		t.Fatalf("Get = %q, %v", v, err)
	}

	// Overwrite an existing key.
	if err := m.Set(ctx, "artifactory/token", "tok-456"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	if v, _ := m.Get(ctx, "artifactory/token"); v != "tok-456" {
		t.Fatalf("Get after overwrite = %q", v)
	}

	namespaces, err := m.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(namespaces)
	want := []string{"artifactory", "database"}
	if len(namespaces) != 2 || namespaces[0] != want[0] || namespaces[1] != want[1] {
		t.Fatalf("namespaces = %v, want %v", namespaces, want)
	}

	keys, err := m.List(ctx, "artifactory")
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %v, %v", keys, err)
	}

	// Unknown namespace: empty slice, nil error.
	empty, err := m.List(ctx, "nope")
	if err != nil || len(empty) != 0 {
		t.Fatalf("List(nope) = %v, %v; want empty, nil", empty, err)
	}

	// Missing secret.
	if _, err := m.Get(ctx, "artifactory/nope"); !errors.Is(err, flexvault.ErrNotFound) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}
	if _, err := m.Get(ctx, "nope/key"); !errors.Is(err, flexvault.ErrNotFound) {
		t.Fatalf("Get missing ns err = %v, want ErrNotFound", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	m, path := newVault(t, "master")
	if err := m.Set(ctx, "ns/key", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Reopen with a fresh driver instance.
	m2 := flexvault.NewManager(New(),
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{CredPassword: "master"})))
	if err := m2.Open(ctx, flexvault.MapDecoder(map[string]any{"path": path})); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer m2.Lock()
	if v, err := m2.Get(ctx, "ns/key"); err != nil || v != "persisted" {
		t.Fatalf("Get after reopen = %q, %v", v, err)
	}
}

func TestBadPasswordIsErrAuth(t *testing.T) {
	ctx := context.Background()
	m, path := newVault(t, "master")
	m.Lock()
	_ = m

	bad := flexvault.NewManager(New(),
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{CredPassword: "wrong"})),
		flexvault.WithUnlockRetries(1))
	err := bad.Open(ctx, flexvault.MapDecoder(map[string]any{"path": path}))
	if !errors.Is(err, flexvault.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestReadOnlyVault(t *testing.T) {
	ctx := context.Background()
	m, path := newVault(t, "master")
	if err := m.Set(ctx, "ns/key", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	m.Lock()

	ro := flexvault.NewManager(New(),
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{CredPassword: "master"})))
	if err := ro.Open(ctx, flexvault.MapDecoder(map[string]any{"path": path, "readonly": true})); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ro.Lock()

	if ro.Capabilities().Writable {
		t.Fatal("readonly vault must not be writable")
	}
	if err := ro.Set(ctx, "ns/key", "x"); !errors.Is(err, flexvault.ErrReadOnly) {
		t.Fatalf("Set err = %v, want ErrReadOnly", err)
	}
	if v, err := ro.Get(ctx, "ns/key"); err != nil || v != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}

func TestInitNeverClobbers(t *testing.T) {
	ctx := context.Background()
	_, path := newVault(t, "master")

	m := flexvault.NewManager(New(),
		flexvault.WithPrompter(flexprompt.NewMapPrompter(map[string]string{CredPassword: "other"})))
	err := m.Create(ctx, flexvault.MapDecoder(map[string]any{"path": path}))
	if err == nil {
		t.Fatal("Create over an existing vault must fail")
	}
}

func TestLockedAccess(t *testing.T) {
	ctx := context.Background()
	d := New()
	if err := d.Configure(flexvault.MapDecoder(map[string]any{"path": "x.kdbx"})); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if _, err := d.Get(ctx, "a/b"); !errors.Is(err, flexvault.ErrLocked) {
		t.Fatalf("Get err = %v, want ErrLocked", err)
	}
	if err := d.Lock(); err != nil {
		t.Fatalf("Lock on locked driver: %v", err)
	}
}
