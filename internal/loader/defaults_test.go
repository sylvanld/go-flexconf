package loader

import (
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

// marshalYAML renders a value the way `settings init` does.
func marshalYAML(v any) (string, error) {
	data, err := yaml.Marshal(v)
	return string(data), err
}

// httpConfig is a typed block used to exercise default merging.
type httpConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout int    `yaml:"timeout"`
	Retries int    `yaml:"retries"`
}

func TestDefaultsDecodeWhenBlockAbsent(t *testing.T) {
	s := Defaults(&httpConfig{BaseURL: "https://api.example.com", Timeout: 30, Retries: 3})

	var got httpConfig
	if err := s.Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := httpConfig{BaseURL: "https://api.example.com", Timeout: 30, Retries: 3}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A loaded block must override only the keys it names, inheriting the rest from
// the default — the merge that makes a partial config file usable.
func TestDefaultsMergeUnderLoadedBlock(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("http:\n  timeout: 5\n")}}

	out := struct {
		HTTP Settings `yaml:"http"`
	}{
		HTTP: Defaults(&httpConfig{BaseURL: "https://api.example.com", Timeout: 30, Retries: 3}),
	}
	if err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}

	var got httpConfig
	if err := out.HTTP.Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := httpConfig{BaseURL: "https://api.example.com", Timeout: 5, Retries: 3}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// UnmarshalYAML must not drop a default already on the field, or the merge above
// silently degrades to a wholesale replacement.
func TestDefaultSurvivesUnmarshal(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("http:\n  timeout: 5\n")}}
	out := struct {
		HTTP Settings `yaml:"http"`
	}{HTTP: Defaults(&httpConfig{BaseURL: "https://api.example.com"})}

	if err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.HTTP.Tree == nil {
		t.Fatal("Tree not captured")
	}
	if out.HTTP.Default == nil {
		t.Error("Default dropped by UnmarshalYAML")
	}
}

// Marshalling a pre-populated config struct is what `settings init` writes.
func TestDefaultsMarshalRendersConfigFile(t *testing.T) {
	cfg := struct {
		HTTP Settings `yaml:"http"`
	}{HTTP: Defaults(&httpConfig{BaseURL: "https://api.example.com", Timeout: 30})}

	data, err := marshalYAML(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{"http:", "base_url: https://api.example.com", "timeout: 30"} {
		if !strings.Contains(data, want) {
			t.Errorf("rendered config missing %q:\n%s", want, data)
		}
	}
}

func TestDecodeWithoutTreeOrDefaultFails(t *testing.T) {
	var s Settings
	if err := s.Decode(&httpConfig{}); err == nil {
		t.Error("want error decoding an empty Settings")
	}
}

// --- Polymorphic defaults ---------------------------------------------------

func newVaults(t *testing.T) *PolymorphicSettings[vault] {
	t.Helper()
	return NewPolymorphicSettings[vault]("type").
		Register("keepass", func() vault { return &keepassVault{Path: "/default.kdbx", ReadOnly: true} }).
		Register("env", func() vault { return &envVault{} }).
		SetDefault("keepass")
}

// An absent block resolves to the default variant, fully populated.
func TestPolymorphicDefaultVariantWhenAbsent(t *testing.T) {
	v, err := newVaults(t).Decode(Settings{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	kv, ok := v.(*keepassVault)
	if !ok {
		t.Fatalf("got %T, want *keepassVault", v)
	}
	if kv.Path != "/default.kdbx" || !kv.ReadOnly {
		t.Errorf("got %+v, want {/default.kdbx true}", kv)
	}
}

// A block omitting the discriminator falls back to the default variant, and the
// factory's pre-populated fields fill in whatever the block leaves out.
func TestPolymorphicFactoryDefaultsFillGaps(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("vault:\n  path: /custom.kdbx\n")}}
	var out struct {
		Vault Settings `yaml:"vault"`
	}
	if err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}

	v, err := newVaults(t).Decode(out.Vault)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	kv, ok := v.(*keepassVault)
	if !ok {
		t.Fatalf("got %T, want *keepassVault", v)
	}
	if kv.Path != "/custom.kdbx" {
		t.Errorf("Path = %q, want /custom.kdbx (block override)", kv.Path)
	}
	if !kv.ReadOnly {
		t.Error("ReadOnly = false, want true (factory default preserved)")
	}
}

// An explicit discriminator still wins over the default variant.
func TestPolymorphicExplicitVariantWinsOverDefault(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("vault:\n  type: env\n  prefix: APP\n")}}
	var out struct {
		Vault Settings `yaml:"vault"`
	}
	if err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, err := newVaults(t).Decode(out.Vault)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := v.(*envVault); !ok {
		t.Fatalf("got %T, want *envVault", v)
	}
}

// Without a default, a missing discriminator stays an error — the pre-existing
// fail-loud behaviour must not be relaxed by adding the default mechanism.
func TestPolymorphicMissingDiscriminatorStillFailsWithoutDefault(t *testing.T) {
	p := NewPolymorphicSettings[vault]("type").
		Register("env", func() vault { return &envVault{} })

	_, err := p.Decode(Defaults(map[string]string{"prefix": "APP"}))
	if err == nil || !strings.Contains(err.Error(), `missing "type"`) {
		t.Errorf("got %v, want a missing-discriminator error", err)
	}
}

// DefaultSettings renders a complete, re-loadable block: the variant's defaults
// plus the discriminator naming it.
func TestPolymorphicDefaultSettingsRendersDiscriminator(t *testing.T) {
	s, err := newVaults(t).DefaultSettings()
	if err != nil {
		t.Fatalf("DefaultSettings: %v", err)
	}
	data, err := marshalYAML(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{"type: keepass", "path: /default.kdbx", "readonly: true"} {
		if !strings.Contains(data, want) {
			t.Errorf("rendered block missing %q:\n%s", want, data)
		}
	}

	// It must round-trip back through Decode to the same variant.
	v, err := newVaults(t).Decode(s)
	if err != nil {
		t.Fatalf("round-trip Decode: %v", err)
	}
	if _, ok := v.(*keepassVault); !ok {
		t.Fatalf("got %T, want *keepassVault", v)
	}
}

func TestSetDefaultUnregisteredPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want panic for an unregistered default variant")
		}
	}()
	NewPolymorphicSettings[vault]("type").SetDefault("nope")
}

func TestDefaultWithoutSetDefaultErrors(t *testing.T) {
	p := NewPolymorphicSettings[vault]("type").
		Register("env", func() vault { return &envVault{} })
	if _, err := p.Default(); err == nil {
		t.Error("want error when no default variant is set")
	}
}
