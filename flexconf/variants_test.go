package flexconf

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The worked example from variants.md §8: notifiers with email/slack variants.
type Notifier interface {
	Notify(ctx context.Context, msg string) error
}

type EmailNotifier struct {
	From    string        `flexconf:"from"`
	Team    string        `flexconf:"team,selector"`
	Timeout time.Duration `flexconf:"timeout"`
}

func (e *EmailNotifier) Notify(context.Context, string) error { return nil }

type SlackNotifier struct {
	Webhook string `flexconf:"webhook"`
	Team    string `flexconf:"team,selector"`
	Channel string `flexconf:"channel"`
}

func (s *SlackNotifier) Notify(context.Context, string) error { return nil }

type notifierAppConfig struct {
	Default Notifier            `flexconf:"default_notifier"`
	Named   map[string]Notifier `flexconf:"notifiers"`
	Fanout  []Notifier          `flexconf:"fanout"`
}

// newNotifierRegistry builds an isolated family registry (no process-global
// leakage between tests).
func newNotifierRegistry() *Registry[Notifier] {
	reg := NewRegistry[Notifier]()
	reg.RegisterVariant("email", func() Notifier { return &EmailNotifier{Timeout: 10 * time.Second} })
	reg.RegisterVariant("slack", func() Notifier { return &SlackNotifier{Channel: "#alerts"} })
	return reg
}

const workedExample = `
default_notifier:
  type: slack
  webhook: https://hooks.example/deploy
  team: platform

notifiers:
  oncall:
    type: email
    from: alerts@example.com
    team: platform
  billing:
    type: email
    from: billing@example.com
    team: finance

fanout:
  - type: slack
    webhook: https://hooks.example/noc
    team: platform
  - type: email
    from: noc@example.com
    team: noc
`

func TestVariantWorkedExample(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", workedExample)
	reg := newNotifierRegistry()

	var cfg notifierAppConfig
	if err := New(dir).WithRegistry(reg).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// (a) struct fields are the primary target.
	slack, ok := cfg.Default.(*SlackNotifier)
	if !ok || slack.Webhook != "https://hooks.example/deploy" {
		t.Fatalf("Default = %#v", cfg.Default)
	}
	if slack.Channel != "#alerts" {
		t.Fatalf("Channel = %q, want factory default #alerts", slack.Channel)
	}
	oncall, ok := cfg.Named["oncall"].(*EmailNotifier)
	if !ok || oncall.From != "alerts@example.com" || oncall.Timeout != 10*time.Second {
		t.Fatalf("oncall = %#v", cfg.Named["oncall"])
	}
	if len(cfg.Fanout) != 2 {
		t.Fatalf("Fanout = %#v", cfg.Fanout)
	}

	// (b) registry resolution by selectors.
	n, err := Resolve[Notifier](reg, Select("name", "oncall"))
	if err != nil || n != cfg.Named["oncall"] {
		t.Fatalf("Resolve(name=oncall) = %#v, %v", n, err)
	}
	n, err = Resolve[Notifier](reg, Select("type", "email"), Select("team", "finance"))
	if err != nil || n != cfg.Named["billing"] {
		t.Fatalf("Resolve(email,finance) = %#v, %v", n, err)
	}

	// Exactly-one rule: team=platform matches three instances.
	_, err = Resolve[Notifier](reg, Select("team", "platform"))
	if !errors.Is(err, ErrVariantAmbiguous) {
		t.Fatalf("err = %v, want ErrVariantAmbiguous", err)
	}
	_, err = Resolve[Notifier](reg, Select("team", "marketing"))
	if !errors.Is(err, ErrVariantNotFound) {
		t.Fatalf("err = %v, want ErrVariantNotFound", err)
	}
}

func TestVariantErrors(t *testing.T) {
	dir := t.TempDir()
	var cfg notifierAppConfig

	t.Run("missing discriminator lists known variants", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "default_notifier: {webhook: x}\n")
		err := New(dir).WithRegistry(newNotifierRegistry()).Load("config.yaml", &cfg)
		if err == nil || !stringsContains(err.Error(), "type is required") ||
			!stringsContains(err.Error(), "email, slack") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unknown variant lists known ones", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "default_notifier: {type: pager}\n")
		err := New(dir).WithRegistry(newNotifierRegistry()).Load("config.yaml", &cfg)
		if err == nil || !stringsContains(err.Error(), `unknown type "pager"`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unknown sub-schema key is strict", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "default_notifier: {type: slack, webook: typo}\n")
		err := New(dir).WithRegistry(newNotifierRegistry()).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrUnknownField) {
			t.Fatalf("err = %v, want ErrUnknownField", err)
		}
	})

	t.Run("discriminator must be a literal, not a token", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "default_notifier: {type: $(env:KIND)}\n")
		l := New(dir).WithRegistry(newNotifierRegistry()).With(env(map[string]string{"KIND": "slack"}))
		err := l.Load("config.yaml", &cfg)
		if err == nil || !stringsContains(err.Error(), "never a token") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("duplicate selector sets rejected at load", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", `
fanout:
  - {type: email, from: a@x, team: same}
  - {type: email, from: b@x, team: same}
`)
		err := New(dir).WithRegistry(newNotifierRegistry()).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrDuplicateVariant) {
			t.Fatalf("err = %v, want ErrDuplicateVariant", err)
		}
	})
}

func TestVariantAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	reg := newNotifierRegistry()
	// First load succeeds and registers one instance.
	writeConfig(t, dir, "config.yaml", "default_notifier: {type: slack, webhook: ok, team: a}\n")
	var cfg notifierAppConfig
	if err := New(dir).WithRegistry(reg).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.InstanceCount() != 1 {
		t.Fatalf("InstanceCount = %d", reg.InstanceCount())
	}

	// A later failing load must not leave its partial instances behind.
	writeConfig(t, dir, "other.yaml", `
notifiers:
  a: {type: email, from: x@x, team: t1}
  b: {type: email, from: y@x, team: t2}
  c: {type: bogus}
`)
	if err := New(dir).WithRegistry(reg).Load("other.yaml", &cfg); err == nil {
		t.Fatal("want failure")
	}
	if reg.InstanceCount() != 1 {
		t.Fatalf("InstanceCount after failed load = %d, want 1 (rolled back)", reg.InstanceCount())
	}
}

type Cache interface{ Cap() int }

type memCache struct {
	Size int `flexconf:"size"`
}

func (m *memCache) Cap() int { return m.Size }

func TestProcessWideRegistry(t *testing.T) {
	RegisterVariant[Cache]("mem", func() Cache { return &memCache{Size: 8} })

	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "cache: {type: mem, size: 32}\n")
	var cfg struct {
		Cache Cache `flexconf:"cache"`
	}
	// No WithRegistry: the Loader routes to the process-wide registry.
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cache.Cap() != 32 {
		t.Fatalf("Cache = %#v", cfg.Cache)
	}
	got, err := Get[Cache](Select("name", "cache"))
	if err != nil || got != cfg.Cache {
		t.Fatalf("Get = %#v, %v", got, err)
	}
	// An unregistered family resolves to not-found.
	type otherFamily interface{ X() }
	if _, err := Get[otherFamily](); !errors.Is(err, ErrVariantNotFound) {
		t.Fatalf("err = %v, want ErrVariantNotFound", err)
	}
}

func TestVariantTokensInSubSchema(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "default_notifier: {type: slack, webhook: $(env:HOOK), team: t}\n")
	var cfg notifierAppConfig
	l := New(dir).WithRegistry(newNotifierRegistry()).With(env(map[string]string{"HOOK": "https://resolved"}))
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default.(*SlackNotifier).Webhook != "https://resolved" {
		t.Fatalf("Default = %#v (sub-schema values may be tokens)", cfg.Default)
	}
}
