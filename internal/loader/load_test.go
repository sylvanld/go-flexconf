package loader

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sylvanld/flexconf/secrets"
	"github.com/sylvanld/flexconf/settings"
)

// mapResolver is a fixed SecretResolver for tests.
type mapResolver map[string]string

func (m mapResolver) Secret(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}

func newSettings(t *testing.T) *settings.Settings {
	t.Helper()
	cfg, err := settings.New("example", settings.WithPath("/cfg"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadTemplatesEnvSecretAndInclude(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte(`
http:
  base_url: $(env:BASE_URL:-https://api.example.com)
  token: $(secret:api/token)
  profile: $(env:HOME)/.cache/example
logging: $(config:logging.yaml)
`)},
		"logging.yaml": {Data: []byte("level: debug\n")},
	}

	var out struct {
		HTTP struct {
			BaseURL string `yaml:"base_url"`
			Token   string `yaml:"token"`
			Profile string `yaml:"profile"`
		} `yaml:"http"`
		Logging struct {
			Level string `yaml:"level"`
		} `yaml:"logging"`
	}

	err := Load(newSettings(t), &out,
		WithFS(fsys),
		WithConfigFile("config.yaml"),
		WithEnv(MapEnv{"HOME": "/home/u"}),
		WithSecretResolver(mapResolver{"api/token": "s3cr3t"}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := out.HTTP.BaseURL, "https://api.example.com"; got != want {
		t.Errorf("base_url = %q, want %q (env default)", got, want)
	}
	if got, want := out.HTTP.Token, "s3cr3t"; got != want {
		t.Errorf("token = %q, want %q (secret)", got, want)
	}
	if got, want := out.HTTP.Profile, "/home/u/.cache/example"; got != want {
		t.Errorf("profile = %q, want %q (embedded env)", got, want)
	}
	if got, want := out.Logging.Level, "debug"; got != want {
		t.Errorf("logging.level = %q, want %q (include)", got, want)
	}
}

// TestLoadDecodesNativeTypes pins the design decision that templated scalars
// are left untagged so they resolve into native Go types — no wrapper types
// needed. A numeric $(env:…) lands in an int/float, "true" in a bool, and a
// string field still gets the literal even when its value looks numeric.
func TestLoadDecodesNativeTypes(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte(`
port: $(env:PORT:-8080)
ratio: $(env:RATIO:-0.25)
debug: $(env:DEBUG:-true)
account: $(secret:account)
nested:
  retries: $(env:RETRIES:-3)
channels:
  - telegram
  - desktop
`)},
	}

	var out struct {
		Port    int     `yaml:"port"`
		Ratio   float64 `yaml:"ratio"`
		Debug   bool    `yaml:"debug"`
		Account string  `yaml:"account"` // numeric-looking secret must stay a string
		Nested  struct {
			Retries int `yaml:"retries"`
		} `yaml:"nested"`
		Channels []string `yaml:"channels"`
	}

	err := Load(newSettings(t), &out,
		WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretResolver(mapResolver{"account": "0042"}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Port != 8080 {
		t.Errorf("port = %d, want 8080 (int)", out.Port)
	}
	if out.Ratio != 0.25 {
		t.Errorf("ratio = %v, want 0.25 (float)", out.Ratio)
	}
	if !out.Debug {
		t.Errorf("debug = %v, want true (bool)", out.Debug)
	}
	if out.Account != "0042" {
		t.Errorf("account = %q, want %q (string keeps leading zero)", out.Account, "0042")
	}
	if out.Nested.Retries != 3 {
		t.Errorf("nested.retries = %d, want 3", out.Nested.Retries)
	}
	if len(out.Channels) != 2 || out.Channels[0] != "telegram" {
		t.Errorf("channels = %v, want [telegram desktop]", out.Channels)
	}
}

func TestLoadRedactsSecrets(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte("token: $(secret:api/token)\nname: public\n")},
	}
	ld, err := LoadFile(newSettings(t),
		WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretResolver(mapResolver{"api/token": "s3cr3t"}),
	)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	dump, err := ld.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if strings.Contains(string(dump), "s3cr3t") {
		t.Errorf("dump leaked the secret:\n%s", dump)
	}
	if !strings.Contains(string(dump), Redacted) {
		t.Errorf("dump missing %q:\n%s", Redacted, dump)
	}
	if !strings.Contains(string(dump), "public") {
		t.Errorf("dump redacted a non-secret:\n%s", dump)
	}
}

func TestLoadUnresolvedSecretIsFatal(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("token: $(secret:missing)\n")}}
	var out map[string]any
	err := Load(newSettings(t), &out,
		WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretResolver(mapResolver{}),
	)
	if err == nil || !strings.Contains(err.Error(), "unresolved $(secret:missing)") {
		t.Fatalf("want unresolved-secret error, got %v", err)
	}
}

func TestLoadSecretInSecretsBlockIsFatal(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("secrets:\n  driver: $(secret:x)\n")}}
	var out map[string]any
	err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml"))
	if err == nil || !strings.Contains(err.Error(), "not allowed in the secrets block") {
		t.Fatalf("want bootstrap error, got %v", err)
	}
}

func TestSecretsBlockSelectsEnvDriver(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte("secrets:\n  driver: env\ntoken: $(secret:api/token)\n")},
	}
	var out struct {
		Token string `yaml:"token"`
	}
	err := Load(newSettings(t), &out,
		WithFS(fsys), WithConfigFile("config.yaml"),
		WithEnv(MapEnv{"API_TOKEN": "from-env"}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Token != "from-env" {
		t.Errorf("token = %q, want %q", out.Token, "from-env")
	}
}

func TestSecretsBlockUnknownDriverIsFatal(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("secrets:\n  driver: nope\n")}}
	var out map[string]any
	err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml"))
	if err == nil || !strings.Contains(err.Error(), `unknown secrets.driver "nope"`) {
		t.Fatalf("want unknown-driver error, got %v", err)
	}
}

func TestSecretsBlockStrippedFromTree(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte("secrets:\n  driver: none\nname: app\n")},
	}
	// A strict-shaped struct without a `secrets` field must still decode: the
	// loader-owned block is removed before the app sees the tree.
	var out struct {
		Name string `yaml:"name"`
	}
	if err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Name != "app" {
		t.Errorf("name = %q, want app", out.Name)
	}
}

func TestIncludeCycleIsFatal(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte("a: $(config:b.yaml)\n")},
		"b.yaml":      {Data: []byte("c: $(config:config.yaml)\n")},
	}
	var out map[string]any
	err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretResolver(mapResolver{}))
	if err == nil || !strings.Contains(err.Error(), "include cycle") {
		t.Fatalf("want include-cycle error, got %v", err)
	}
}

func TestConfigPathFromAppEnv(t *testing.T) {
	fsys := fstest.MapFS{"custom.yaml": {Data: []byte("name: viaenv\n")}}
	var out struct {
		Name string `yaml:"name"`
	}
	// No WithConfigFile: path comes from EXAMPLE_CONFIG.
	err := Load(newSettings(t), &out,
		WithFS(fsys),
		WithEnv(MapEnv{"EXAMPLE_CONFIG": "custom.yaml"}),
		WithSecretResolver(mapResolver{}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Name != "viaenv" {
		t.Errorf("name = %q, want viaenv", out.Name)
	}
}

func TestUnknownNamespaceIsFatal(t *testing.T) {
	fsys := fstest.MapFS{"config.yaml": {Data: []byte("x: $(foo:bar)\n")}}
	var out map[string]any
	err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretResolver(mapResolver{}))
	if err == nil || !strings.Contains(err.Error(), "unknown namespace") {
		t.Fatalf("want unknown-namespace error, got %v", err)
	}
}

func TestInjectedStoreWins(t *testing.T) {
	fsys := fstest.MapFS{
		// driver: none would make api/token unresolved; the injected store overrides.
		"config.yaml": {Data: []byte("secrets:\n  driver: none\ntoken: $(secret:api/token)\n")},
	}
	store := secrets.NewStore(mapDriver{"api/token": "injected"})
	var out struct {
		Token string `yaml:"token"`
	}
	err := Load(newSettings(t), &out, WithFS(fsys), WithConfigFile("config.yaml"),
		WithSecretStore(store))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Token != "injected" {
		t.Errorf("token = %q, want injected", out.Token)
	}
}

// mapDriver is a minimal read-only secrets.Driver for the injected-store test.
type mapDriver map[string]string

func (mapDriver) Unlock() error { return nil }
func (m mapDriver) Get(key string) (*secrets.Secret, error) {
	if v, ok := m[key]; ok {
		return &secrets.Secret{Key: key, Value: v}, nil
	}
	return nil, secrets.ErrNotFound
}
func (mapDriver) Set(secrets.Secret) error        { return secrets.ErrReadOnly }
func (mapDriver) List() ([]secrets.Secret, error) { return nil, nil }
func (mapDriver) Delete(string) error             { return secrets.ErrReadOnly }
