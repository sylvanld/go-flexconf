package flexconf

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig creates name in dir with the given content.
func writeConfig(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type appConfig struct {
	Service string         `flexconf:"service,required"`
	Port    int            `flexconf:"port"`
	Debug   bool           `flexconf:"debug"`
	Timeout time.Duration  `flexconf:"timeout"`
	Rate    float64        `flexconf:"rate"`
	Tags    []string       `flexconf:"tags"`
	Extra   map[string]int `flexconf:"extra"`
	Sub     struct {
		URL   string `flexconf:"url"`
		Token string `flexconf:"token"`
	} `flexconf:"sub"`
}

func TestLoadBasic(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
service: api
port: 8080
debug: true
timeout: 30s
rate: 0.5
tags: [a, b]
extra: {x: 1, y: 2}
sub:
  url: https://example.com
  token: shhh
`)
	var cfg appConfig
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Service != "api" || cfg.Port != 8080 || !cfg.Debug ||
		cfg.Timeout != 30*time.Second || cfg.Rate != 0.5 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[1] != "b" || cfg.Extra["y"] != 2 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Sub.URL != "https://example.com" || cfg.Sub.Token != "shhh" {
		t.Fatalf("sub = %+v", cfg.Sub)
	}
}

func TestNameValidation(t *testing.T) {
	l := New(t.TempDir())
	var cfg appConfig
	if err := l.Load("config.toml", &cfg); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	for _, bad := range []string{"a/b.yaml", "../x.yaml", `a\b.yaml`, ""} {
		if err := l.Load(bad, &cfg); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Load(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}
}

func TestNewPanicsWithoutDirs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New() without dirs should panic")
		}
	}()
	New()
}

func TestConfigNotFound(t *testing.T) {
	var cfg appConfig
	err := New(t.TempDir()).Load("missing.yaml", &cfg)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestEmptyFileIsPresent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "")
	var cfg struct {
		Port int `flexconf:"port"`
	}
	cfg.Port = 9 // default survives an empty file
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9 {
		t.Fatalf("Port = %d, want default 9", cfg.Port)
	}
}

func TestLayering(t *testing.T) {
	base, override := t.TempDir(), t.TempDir()
	writeConfig(t, base, "config.yaml", `
service: api
port: 8080
tags: [a, b]
sub: {url: base-url, token: base-token}
`)
	writeConfig(t, override, "config.yaml", `
port: 9090
tags: [c]
sub: {token: override-token}
`)
	var cfg appConfig
	if err := New(base, override).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Later dir wins per key; maps deep-merge; sequences replace wholesale.
	if cfg.Service != "api" || cfg.Port != 9090 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Tags) != 1 || cfg.Tags[0] != "c" {
		t.Fatalf("Tags = %v (sequences must replace, not append)", cfg.Tags)
	}
	if cfg.Sub.URL != "base-url" || cfg.Sub.Token != "override-token" {
		t.Fatalf("Sub = %+v (maps must deep-merge)", cfg.Sub)
	}
}

func TestPerFileShapeValidation(t *testing.T) {
	base, override := t.TempDir(), t.TempDir()
	writeConfig(t, base, "config.yaml", "service: api\nsub: {url: x}\n")
	writeConfig(t, override, "config.yaml", "sub: just-a-string\n")
	var cfg appConfig
	err := New(base, override).Load("config.yaml", &cfg)
	if err == nil {
		t.Fatal("shape conflict should fail")
	}
	if !stringsContains(err.Error(), filepath.Join(override, "config.yaml")) {
		t.Fatalf("err %q should name the offending file", err)
	}
}

func stringsContains(s, sub string) bool { return strings.Contains(s, sub) }

func TestStrictUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "service: api\nservise: typo\n")
	var cfg appConfig
	err := New(dir).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("err = %v, want ErrUnknownField", err)
	}
	if !stringsContains(err.Error(), "servise") {
		t.Fatalf("err %q should name the unknown key", err)
	}
}

func TestRequired(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "port: 1\n")
	var cfg appConfig
	err := New(dir).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("err = %v, want ErrMissingRequired", err)
	}

	// Presence is what counts: an explicit empty value satisfies required.
	writeConfig(t, dir, "config.yaml", "service: \"\"\n")
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("explicit empty should satisfy required: %v", err)
	}
}

func TestDefaultsPrePopulated(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "service: api\nport: 0\n")
	cfg := appConfig{Port: 1234, Timeout: 9 * time.Second}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// port: 0 present in the file overrides to 0 (presence-driven merge);
	// absent timeout keeps the default.
	if cfg.Port != 0 {
		t.Fatalf("Port = %d, want explicit 0", cfg.Port)
	}
	if cfg.Timeout != 9*time.Second {
		t.Fatalf("Timeout = %v, want default 9s", cfg.Timeout)
	}
}

func TestTypeMismatchNamesFieldAndValue(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "service: api\nport: not-a-number\n")
	var cfg appConfig
	err := New(dir).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
	if !stringsContains(err.Error(), "port") || !stringsContains(err.Error(), "not-a-number") {
		t.Fatalf("err %q should name key path and value", err)
	}
}

func TestAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "service: api\nport: bad\n")
	cfg := appConfig{Service: "keep", Port: 1}
	err := New(dir).Load("config.yaml", &cfg)
	if err == nil {
		t.Fatal("want error")
	}
	if cfg.Service != "keep" || cfg.Port != 1 {
		t.Fatalf("dst mutated on failure: %+v", cfg)
	}
}

type embedded struct {
	Region string `flexconf:"region"`
	Env    string `flexconf:"env"`
}

func TestEmbeddingInlinesByDefault(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "name: api\nregion: eu\nenv: prod\n")
	var cfg struct {
		embedded
		Name string `flexconf:"name"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Region != "eu" || cfg.Env != "prod" || cfg.Name != "api" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestEmbeddingNamedNests(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "name: api\ncommon: {region: eu, env: prod}\n")
	var cfg struct {
		Common embedded `flexconf:"common"`
		Name   string   `flexconf:"name"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Common.Region != "eu" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// logLevel is a custom scalar type using the encoding.TextUnmarshaler
// extension point.
type logLevel int

func (l *logLevel) UnmarshalText(text []byte) error {
	switch string(text) {
	case "debug":
		*l = 1
	case "info":
		*l = 2
	default:
		return fmt.Errorf("unknown level %q", text)
	}
	return nil
}

func TestTextUnmarshaler(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "level: info\nurl: https://example.com/api\n")
	var cfg struct {
		Level logLevel `flexconf:"level"`
		URL   url.URL  `flexconf:"-"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err == nil {
		t.Fatal("url key should be unknown (field skipped)")
	}
	writeConfig(t, dir, "config.yaml", "level: info\n")
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Level != 2 {
		t.Fatalf("Level = %d, want 2", cfg.Level)
	}

	// A value the unmarshaler rejects is a type mismatch naming the value.
	writeConfig(t, dir, "config.yaml", "level: loud\n")
	err := New(dir).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrTypeMismatch) || !stringsContains(err.Error(), "loud") {
		t.Fatalf("err = %v, want ErrTypeMismatch naming the value", err)
	}
}

type validatingConfig struct {
	Timeout time.Duration   `flexconf:"timeout"`
	Nested  innerValidating `flexconf:"nested"`
}

func (c *validatingConfig) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	return nil
}

type innerValidating struct {
	Level int `flexconf:"level"`
	order *[]string
}

func (i *innerValidating) Validate() error {
	if i.Level > 10 {
		return fmt.Errorf("level too high")
	}
	return nil
}

func TestValidateHook(t *testing.T) {
	dir := t.TempDir()

	t.Run("root hook failure", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "timeout: -5s\nnested: {level: 1}\n")
		var cfg validatingConfig
		err := New(dir).Load("config.yaml", &cfg)
		if err == nil || !stringsContains(err.Error(), "timeout must be positive") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("nested hook failure wraps key path", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "timeout: 5s\nnested: {level: 99}\n")
		var cfg validatingConfig
		err := New(dir).Load("config.yaml", &cfg)
		if err == nil || !stringsContains(err.Error(), "level too high") || !stringsContains(err.Error(), "nested") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "timeout: 5s\nnested: {level: 1}\n")
		var cfg validatingConfig
		if err := New(dir).Load("config.yaml", &cfg); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})
}

func TestPointerAndAnyTargets(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
opt: {value: 5}
blob:
  num: 3
  str: hello
  flag: true
  list: [1, two]
`)
	var cfg struct {
		Opt *struct {
			Value int `flexconf:"value"`
		} `flexconf:"opt"`
		Blob map[string]any `flexconf:"blob"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Opt == nil || cfg.Opt.Value != 5 {
		t.Fatalf("Opt = %+v", cfg.Opt)
	}
	if cfg.Blob["num"] != int64(3) || cfg.Blob["str"] != "hello" || cfg.Blob["flag"] != true {
		t.Fatalf("Blob = %#v", cfg.Blob)
	}
	list, ok := cfg.Blob["list"].([]any)
	if !ok || list[0] != int64(1) || list[1] != "two" {
		t.Fatalf("list = %#v", cfg.Blob["list"])
	}
}

func TestQuotedScalarsStayStrings(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `v: "8080"`)
	var cfg struct {
		V any `flexconf:"v"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.V != "8080" {
		t.Fatalf("V = %#v, want string \"8080\"", cfg.V)
	}
}

func TestInvalidTag(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "x: 1\n")
	var cfg struct {
		X int `flexconf:"x,bogus"`
	}
	err := New(dir).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("err = %v, want ErrInvalidTag", err)
	}
}

func TestSkippedAndUnexportedFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "x: 1\n")
	var cfg struct {
		X       int    `flexconf:"x"`
		Skip    string `flexconf:"-"`
		private string
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A key matching a skipped field name is unknown.
	writeConfig(t, dir, "config.yaml", "x: 1\nskip: nope\n")
	if err := New(dir).Load("config.yaml", &cfg); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("err = %v, want ErrUnknownField", err)
	}
	_ = cfg.private
}

func TestConcurrentLoads(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "service: api\n")
	l := New(dir)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			var cfg appConfig
			done <- l.Load("config.yaml", &cfg)
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Load: %v", err)
		}
	}
}
