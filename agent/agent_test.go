package agent

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"forgejo.ovhcloud.tools/sylvan/flexconf/secrets"
)

// fakeDriver is an in-memory secrets.Driver for tests (no KeePass, no password).
type fakeDriver struct {
	mu sync.Mutex
	m  map[string]secrets.Secret
}

func newFake() *fakeDriver { return &fakeDriver{m: map[string]secrets.Secret{}} }

func (f *fakeDriver) Unlock() error { return nil }

func (f *fakeDriver) Get(key string) (*secrets.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[key]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	cp := s
	return &cp, nil
}

func (f *fakeDriver) Set(s secrets.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[s.Key] = s
	return nil
}

func (f *fakeDriver) List() ([]secrets.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]secrets.Secret, 0, len(f.m))
	for _, s := range f.m {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeDriver) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[key]; !ok {
		return secrets.ErrNotFound
	}
	delete(f.m, key)
	return nil
}

func waitRunning(client *Client, want bool) bool {
	for i := 0; i < 200; i++ {
		if client.IsRunning() == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func startServer(t *testing.T, drv secrets.Driver, idle, max time.Duration) (*Server, *Client) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "agent.sock")
	srv := NewServer(drv, sock)
	srv.IdleTTL = idle
	srv.MaxLifetime = max
	go func() { _ = srv.Serve() }()
	client := NewClient(sock)
	if !waitRunning(client, true) {
		t.Fatal("agent did not start")
	}
	t.Cleanup(func() { srv.Stop() })
	return srv, client
}

func TestAgentRoundTrip(t *testing.T) {
	fake := newFake()
	fake.m["a/b"] = secrets.Secret{Key: "a/b", Value: "v1"}
	_, client := startServer(t, fake, 5*time.Second, 30*time.Second)

	// Get through a Store wrapping the client driver (same-uid connection also
	// exercises the SO_PEERCRED accept path on Linux).
	store := secrets.NewStore(client)
	v, err := store.GetValue("a/b")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if *v != "v1" {
		t.Fatalf("GetValue = %q, want v1", *v)
	}

	// ErrNotFound must survive the wire.
	if _, err := store.Get("missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}

	// List.
	list, err := client.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Key != "a/b" {
		t.Fatalf("List = %+v", list)
	}

	// Writes go through the agent.
	if err := store.SetValue("x/y", "z"); err != nil {
		t.Fatalf("SetValue via agent: %v", err)
	}
	got, err := store.GetValue("x/y")
	if err != nil {
		t.Fatalf("GetValue after Set: %v", err)
	}
	if *got != "z" {
		t.Fatalf("GetValue = %q, want z", *got)
	}
	if err := client.Delete("a/b"); err != nil {
		t.Fatalf("Delete via agent: %v", err)
	}
	if _, err := client.Get("a/b"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get after Delete err = %v, want ErrNotFound", err)
	}
}

func TestAgentIdleExpiry(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "agent.sock")
	srv := NewServer(newFake(), sock)
	srv.IdleTTL = 150 * time.Millisecond
	srv.MaxLifetime = 10 * time.Second

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()

	client := NewClient(sock)
	if !waitRunning(client, true) {
		t.Fatal("agent did not start")
	}
	if !waitRunning(client, false) {
		t.Fatal("agent did not expire after idle TTL")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after expiry")
	}
}

func TestAgentLock(t *testing.T) {
	_, client := startServer(t, newFake(), 10*time.Second, 30*time.Second)
	if err := client.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !waitRunning(client, false) {
		t.Fatal("agent still running after lock")
	}
}
