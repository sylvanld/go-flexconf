package flexvault

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sylvanld/go-flexconf/flexprompt"
)

// fakeDriver is an in-memory VaultDriver for Manager tests.
type fakeDriver struct {
	cfg           struct{ Path string }
	password      string // expected password
	store         map[string]string
	readonly      bool
	unlocked      bool
	unlockCalls   int
	creatable     bool
	created       bool
	initPassword  string
	failConfigure error
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{password: "good", store: map[string]string{"ns/key": "v1"}}
}

func (d *fakeDriver) Name() string { return "fake" }

func (d *fakeDriver) Configure(decode func(any) error) error {
	if d.failConfigure != nil {
		return d.failConfigure
	}
	var c struct {
		Path string `flexconf:"path"`
	}
	if err := decode(&c); err != nil {
		return err
	}
	d.cfg.Path = c.Path
	return nil
}

func (d *fakeDriver) Credentials() []flexprompt.PromptRequest {
	return []flexprompt.PromptRequest{{ID: "password", Secret: true}}
}

func (d *fakeDriver) Unlock(_ context.Context, answers map[string]string) error {
	d.unlockCalls++
	if answers["password"] != d.password {
		return fmt.Errorf("fake: bad password: %w", ErrAuth)
	}
	d.unlocked = true
	return nil
}

func (d *fakeDriver) Capabilities() Capabilities {
	return Capabilities{Writable: !d.readonly, Creatable: d.creatable}
}

func (d *fakeDriver) Get(_ context.Context, addr string) (string, error) {
	v, ok := d.store[addr]
	if !ok {
		return "", fmt.Errorf("fake: %s: %w", addr, ErrNotFound)
	}
	return v, nil
}

func (d *fakeDriver) Set(_ context.Context, addr, value string) error {
	if d.readonly {
		return ErrReadOnly
	}
	d.store[addr] = value
	return nil
}

func (d *fakeDriver) List(_ context.Context, namespace string) ([]string, error) {
	if namespace == "" {
		return []string{"ns"}, nil
	}
	return []string{"key"}, nil
}

func (d *fakeDriver) Lock() error {
	d.unlocked = false
	return nil
}

func (d *fakeDriver) InitCredentials() []flexprompt.PromptRequest {
	return []flexprompt.PromptRequest{{ID: "password", Secret: true, Confirm: true}}
}

func (d *fakeDriver) Init(_ context.Context, answers map[string]string) error {
	if d.created {
		return errors.New("fake: vault already exists")
	}
	d.created = true
	d.initPassword = answers["password"]
	return nil
}

func openManager(t *testing.T, d VaultDriver, password string, opts ...Option) *Manager {
	t.Helper()
	opts = append(opts, WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": password})))
	m := NewManager(d, opts...)
	if err := m.Open(context.Background(), MapDecoder(map[string]any{"path": "x.kdbx"})); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return m
}

func TestManagerLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("pre-configure calls return ErrNotConfigured", func(t *testing.T) {
		m := NewManager(newFakeDriver())
		if err := m.Unlock(ctx); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Unlock err = %v, want ErrNotConfigured", err)
		}
		if _, err := m.Get(ctx, "ns/key"); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Get err = %v, want ErrNotConfigured", err)
		}
	})

	t.Run("configured but locked returns ErrLocked", func(t *testing.T) {
		m := NewManager(newFakeDriver())
		if err := m.Configure(MapDecoder(map[string]any{"path": "x"})); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Get(ctx, "ns/key"); !errors.Is(err, ErrLocked) {
			t.Fatalf("Get err = %v, want ErrLocked", err)
		}
	})

	t.Run("open then get/set/list/lock", func(t *testing.T) {
		d := newFakeDriver()
		m := openManager(t, d, "good")
		if !m.IsUnlocked() {
			t.Fatal("should be unlocked")
		}
		v, err := m.Get(ctx, "ns/key")
		if err != nil || v != "v1" {
			t.Fatalf("Get = %q, %v", v, err)
		}
		if err := m.Set(ctx, "ns/other", "v2"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if names, err := m.List(ctx, ""); err != nil || len(names) != 1 {
			t.Fatalf("List = %v, %v", names, err)
		}
		if err := m.Lock(); err != nil {
			t.Fatalf("Lock: %v", err)
		}
		if _, err := m.Get(ctx, "ns/key"); !errors.Is(err, ErrLocked) {
			t.Fatalf("Get after Lock err = %v, want ErrLocked", err)
		}
	})

	t.Run("unlock when already unlocked is a no-op", func(t *testing.T) {
		d := newFakeDriver()
		m := openManager(t, d, "good")
		calls := d.unlockCalls
		if err := m.Unlock(ctx); err != nil {
			t.Fatalf("re-Unlock: %v", err)
		}
		if d.unlockCalls != calls {
			t.Fatal("re-Unlock must not re-prompt/re-unlock")
		}
	})
}

func TestManagerRetries(t *testing.T) {
	ctx := context.Background()

	t.Run("ErrAuth retries up to cap", func(t *testing.T) {
		d := newFakeDriver()
		m := NewManager(d,
			WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "bad"})),
			WithUnlockRetries(3))
		err := m.Open(ctx, MapDecoder(map[string]any{"path": "x"}))
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
		if d.unlockCalls != 3 {
			t.Fatalf("unlockCalls = %d, want 3", d.unlockCalls)
		}
	})

	t.Run("prompter error is terminal", func(t *testing.T) {
		d := newFakeDriver()
		cancelled := flexprompt.PrompterFunc(func(context.Context, []flexprompt.PromptRequest) (map[string]string, error) {
			return nil, flexprompt.ErrPromptCancelled
		})
		m := NewManager(d, WithPrompter(cancelled))
		err := m.Open(ctx, MapDecoder(map[string]any{"path": "x"}))
		if !errors.Is(err, flexprompt.ErrPromptCancelled) {
			t.Fatalf("err = %v, want ErrPromptCancelled", err)
		}
		if d.unlockCalls != 0 {
			t.Fatal("driver.Unlock must not run after cancelled prompt")
		}
	})

	t.Run("non-auth unlock error does not retry", func(t *testing.T) {
		d := newFakeDriver()
		d.password = "good"
		boom := flexprompt.NewMapPrompter(map[string]string{"password": "good"})
		d.failConfigure = nil
		m := NewManager(d, WithPrompter(boom))
		// Make Unlock fail with a non-auth error by pre-locking store access:
		// simulate via a driver whose Unlock errors non-auth.
		d2 := &nonAuthFailDriver{fakeDriver: d}
		m = NewManager(d2, WithPrompter(boom), WithUnlockRetries(3))
		err := m.Open(ctx, MapDecoder(map[string]any{"path": "x"}))
		if err == nil || errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want non-auth error", err)
		}
		if d2.calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry)", d2.calls)
		}
	})
}

type nonAuthFailDriver struct {
	*fakeDriver
	calls int
}

func (d *nonAuthFailDriver) Unlock(context.Context, map[string]string) error {
	d.calls++
	return errors.New("backend file corrupt")
}

func TestManagerAddressValidation(t *testing.T) {
	ctx := context.Background()
	m := openManager(t, newFakeDriver(), "good")
	for _, bad := range []string{"", "nokey", "/key", "ns/", "a/b/c", "ns//k"} {
		if _, err := m.Get(ctx, bad); err == nil {
			t.Fatalf("Get(%q) should fail", bad)
		}
	}
	// Whitespace is trimmed before validation.
	if v, err := m.Get(ctx, "  ns/key  "); err != nil || v != "v1" {
		t.Fatalf("trimmed Get = %q, %v", v, err)
	}
}

func TestManagerCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("create via Initializer", func(t *testing.T) {
		d := newFakeDriver()
		d.creatable = true
		m := NewManager(d, WithPrompter(flexprompt.NewMapPrompter(map[string]string{"password": "new"})))
		if err := m.Create(ctx, MapDecoder(map[string]any{"path": "x"})); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !d.created || d.initPassword != "new" {
			t.Fatalf("created=%v initPassword=%q", d.created, d.initPassword)
		}
		if m.IsUnlocked() {
			t.Fatal("Create must not leave the vault unlocked")
		}
	})

	t.Run("driver without Initializer returns ErrUnsupported", func(t *testing.T) {
		m := NewManager(noInitDriver{newFakeDriver()})
		err := m.Create(ctx, MapDecoder(nil))
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	})
}

// noInitDriver hides the fakeDriver's Initializer methods.
type noInitDriver struct{ d *fakeDriver }

func (n noInitDriver) Name() string                            { return n.d.Name() }
func (n noInitDriver) Configure(f func(any) error) error       { return n.d.Configure(f) }
func (n noInitDriver) Credentials() []flexprompt.PromptRequest { return n.d.Credentials() }
func (n noInitDriver) Unlock(ctx context.Context, a map[string]string) error {
	return n.d.Unlock(ctx, a)
}
func (n noInitDriver) Capabilities() Capabilities { return n.d.Capabilities() }
func (n noInitDriver) Get(ctx context.Context, addr string) (string, error) {
	return n.d.Get(ctx, addr)
}
func (n noInitDriver) Set(ctx context.Context, addr, v string) error { return n.d.Set(ctx, addr, v) }
func (n noInitDriver) List(ctx context.Context, ns string) ([]string, error) {
	return n.d.List(ctx, ns)
}
func (n noInitDriver) Lock() error { return n.d.Lock() }

func TestRegistration(t *testing.T) {
	Register("testdrv", func() VaultDriver { return newFakeDriver() })

	d, err := New("testdrv")
	if err != nil || d.Name() != "fake" {
		t.Fatalf("New = %v, %v", d, err)
	}
	if _, err := New("nope"); err == nil {
		t.Fatal("unknown driver should error")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate Register should panic")
			}
		}()
		Register("testdrv", func() VaultDriver { return newFakeDriver() })
	}()
}

func TestDecoders(t *testing.T) {
	type cfg struct {
		Path     string `flexconf:"path"`
		ReadOnly bool   `flexconf:"readonly"`
		Retries  int
		Skipped  string `flexconf:"-"`
	}

	t.Run("MapDecoder binds and rejects unknown keys", func(t *testing.T) {
		var c cfg
		err := MapDecoder(map[string]any{"path": "a.kdbx", "readonly": true, "retries": 2})(&c)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if c.Path != "a.kdbx" || !c.ReadOnly || c.Retries != 2 {
			t.Fatalf("c = %+v", c)
		}
		if err := MapDecoder(map[string]any{"nope": 1})(&c); err == nil {
			t.Fatal("unknown key should error")
		}
		if err := MapDecoder(map[string]any{"skipped": "x"})(&c); err == nil {
			t.Fatal("skipped field key should be unknown")
		}
	})

	t.Run("MapDecoder string coercion", func(t *testing.T) {
		var c cfg
		if err := MapDecoder(map[string]any{"readonly": "true", "retries": "5"})(&c); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !c.ReadOnly || c.Retries != 5 {
			t.Fatalf("c = %+v", c)
		}
	})

	t.Run("EnvDecoder binds from environment", func(t *testing.T) {
		t.Setenv("FVTEST_PATH", "env.kdbx")
		t.Setenv("FVTEST_READONLY", "true")
		var c cfg
		if err := EnvDecoder("FVTEST_")(&c); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if c.Path != "env.kdbx" || !c.ReadOnly {
			t.Fatalf("c = %+v", c)
		}
	})

	t.Run("non-struct target errors", func(t *testing.T) {
		var s string
		if err := MapDecoder(nil)(&s); err == nil {
			t.Fatal("non-struct target should error")
		}
		if err := MapDecoder(nil)(nil); err == nil {
			t.Fatal("nil target should error")
		}
	})
}

func TestParseAddress(t *testing.T) {
	ns, key, err := ParseAddress("artifactory/token")
	if err != nil || ns != "artifactory" || key != "token" {
		t.Fatalf("ParseAddress = %q, %q, %v", ns, key, err)
	}
	for _, bad := range []string{"", "x", "/x", "x/", "a/b/c"} {
		if _, _, err := ParseAddress(bad); err == nil {
			t.Fatalf("ParseAddress(%q) should fail", bad)
		}
	}
}
