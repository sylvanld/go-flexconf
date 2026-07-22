package variant

import (
	"errors"
	"strings"
	"testing"
)

type store interface{ Kind() string }

type redisStore struct {
	Region  string
	Timeout int
}

func (r *redisStore) Kind() string { return "redis" }

type memoryStore struct{}

func (m *memoryStore) Kind() string { return "memory" }

func newFamily(t *testing.T) *Registry[store] {
	t.Helper()
	reg := NewRegistry[store]()
	reg.RegisterVariant("redis", func() store { return &redisStore{Timeout: 5} })
	reg.RegisterVariant("memory", func() store { return &memoryStore{} })
	return reg
}

func TestRegisterVariant(t *testing.T) {
	reg := newFamily(t)

	if got := reg.Variants(); len(got) != 2 || got[0] != "memory" || got[1] != "redis" {
		t.Fatalf("Variants = %v", got)
	}

	t.Run("factory returns pre-populated defaults", func(t *testing.T) {
		v, err := reg.New("redis")
		if err != nil {
			t.Fatal(err)
		}
		if v.(*redisStore).Timeout != 5 {
			t.Fatalf("Timeout = %d, want default 5", v.(*redisStore).Timeout)
		}
	})

	t.Run("unknown variant lists known ones", func(t *testing.T) {
		_, err := reg.New("cassandra")
		if err == nil || !strings.Contains(err.Error(), "memory, redis") {
			t.Fatalf("err = %v, want known variants listed", err)
		}
	})

	t.Run("duplicate registration panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("want panic")
			}
		}()
		reg.RegisterVariant("redis", func() store { return &redisStore{} })
	})
}

func TestDiscriminator(t *testing.T) {
	def := NewRegistry[store]()
	if def.Discriminator() != "type" {
		t.Fatalf("default discriminator = %q, want type", def.Discriminator())
	}
	vault := NewRegistry[store](WithDiscriminator("driver"))
	if vault.Discriminator() != "driver" {
		t.Fatalf("discriminator = %q, want driver", vault.Discriminator())
	}
	unknownErr := func() error {
		_, err := vault.New("x")
		return err
	}()
	if !strings.Contains(unknownErr.Error(), "unknown driver") {
		t.Fatalf("err %q should name the discriminator", unknownErr)
	}
}

func addInstance(t *testing.T, reg *Registry[store], v store, sel map[string]string, loc string) {
	t.Helper()
	if err := reg.AddInstance(v, sel, loc); err != nil {
		t.Fatalf("AddInstance(%s): %v", loc, err)
	}
}

func TestResolution(t *testing.T) {
	reg := newFamily(t)
	eu := &redisStore{Region: "eu"}
	us := &redisStore{Region: "us"}
	mem := &memoryStore{}
	addInstance(t, reg, eu, map[string]string{"type": "redis", "region": "eu"}, "cfg:options[0]")
	addInstance(t, reg, us, map[string]string{"type": "redis", "region": "us"}, "cfg:options[1]")
	addInstance(t, reg, mem, map[string]string{"type": "memory", "name": "cache"}, "cfg:cache")

	t.Run("exactly one match", func(t *testing.T) {
		v, err := reg.Resolve(Select("region", "eu"))
		if err != nil || v != store(eu) {
			t.Fatalf("Resolve = %v, %v", v, err)
		}
		v, err = reg.Resolve(Select("name", "cache"))
		if err != nil || v != store(mem) {
			t.Fatalf("Resolve = %v, %v", v, err)
		}
	})

	t.Run("subset matching combines selectors", func(t *testing.T) {
		v, err := reg.Resolve(Select("type", "redis"), Select("region", "us"))
		if err != nil || v != store(us) {
			t.Fatalf("Resolve = %v, %v", v, err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := reg.Resolve(Select("region", "apac"))
		if !errors.Is(err, ErrVariantNotFound) {
			t.Fatalf("err = %v, want ErrVariantNotFound", err)
		}
	})

	t.Run("ambiguous match lists candidates", func(t *testing.T) {
		_, err := reg.Resolve(Select("type", "redis"))
		if !errors.Is(err, ErrVariantAmbiguous) {
			t.Fatalf("err = %v, want ErrVariantAmbiguous", err)
		}
		if !strings.Contains(err.Error(), "region=eu") || !strings.Contains(err.Error(), "region=us") {
			t.Fatalf("err %q should list the matching selector sets", err)
		}
	})

	t.Run("empty query over multiple instances is ambiguous", func(t *testing.T) {
		_, err := reg.Resolve()
		if !errors.Is(err, ErrVariantAmbiguous) {
			t.Fatalf("err = %v, want ErrVariantAmbiguous", err)
		}
	})
}

func TestDuplicateInstanceRejected(t *testing.T) {
	reg := newFamily(t)
	sel := map[string]string{"type": "redis", "region": "eu"}
	addInstance(t, reg, &redisStore{}, sel, "cfg:a")
	err := reg.AddInstance(&redisStore{}, map[string]string{"region": "eu", "type": "redis"}, "cfg:b")
	if !errors.Is(err, ErrDuplicateVariant) {
		t.Fatalf("err = %v, want ErrDuplicateVariant", err)
	}
	if !strings.Contains(err.Error(), "cfg:a") || !strings.Contains(err.Error(), "cfg:b") {
		t.Fatalf("err %q should name both locations", err)
	}
}

func TestClearInstances(t *testing.T) {
	reg := newFamily(t)
	addInstance(t, reg, &memoryStore{}, map[string]string{"type": "memory"}, "cfg:x")
	if reg.InstanceCount() != 1 {
		t.Fatalf("InstanceCount = %d", reg.InstanceCount())
	}
	reg.ClearInstances()
	if reg.InstanceCount() != 0 {
		t.Fatal("ClearInstances should drop all instances")
	}
	if _, err := reg.Resolve(Select("type", "memory")); !errors.Is(err, ErrVariantNotFound) {
		t.Fatalf("err = %v, want ErrVariantNotFound after clear", err)
	}
}
