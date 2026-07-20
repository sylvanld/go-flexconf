package loader

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/sylvanld/go-flexconf/agent"
	"github.com/sylvanld/go-flexconf/secrets"
	"github.com/sylvanld/go-flexconf/settings"
)

// SecretResolver resolves a $(secret:NAME) reference to its value. The name is
// the whole path-like key as written (e.g. "api/token"). A missing key must
// surface as secrets.ErrNotFound so the templating pass can raise one uniform
// "unresolved $(secret:NAME)" error with the config file:line; any other error
// (locked driver, wrong password, agent unreachable) propagates verbatim.
type SecretResolver interface {
	Secret(name string) (value string, err error)
}

// StoreResolver adapts a *secrets.Store to a SecretResolver. It calls
// Store.GetValue, which lazily Unlock()s the driver on first use, so unlocking
// (a KeePass password prompt, an agent handshake) happens exactly once, on the
// first $(secret:…) hit — not at config-file open.
func StoreResolver(s *secrets.Store) SecretResolver { return storeResolver{s} }

type storeResolver struct{ s *secrets.Store }

func (r storeResolver) Secret(name string) (string, error) {
	v, err := r.s.GetValue(name)
	if err != nil {
		return "", err
	}
	return *v, nil
}

// SecretDriverFactory builds a secrets.Driver from its config sub-block. It
// gets the resolved *settings.AppConfig (for defaults like
// cfg.File("secrets.kdbx")), the driver's own opts node — already templated
// with env+config only — and the loader Env (for the env driver / tests).
type SecretDriverFactory func(cfg *settings.AppConfig, opts *yaml.Node, env Env) (secrets.Driver, error)

var (
	driverMu        sync.RWMutex
	driverFactories = map[string]SecretDriverFactory{}
)

// RegisterSecretDriver registers a driver factory under name, panicking on a
// duplicate — the same registry idiom as the rest of the toolkit. An unknown
// name in a config is a fatal load error.
func RegisterSecretDriver(name string, f SecretDriverFactory) {
	driverMu.Lock()
	defer driverMu.Unlock()
	if _, dup := driverFactories[name]; dup {
		panic(fmt.Sprintf("flexconf: duplicate secret driver %q", name))
	}
	driverFactories[name] = f
}

func secretDriverFactory(name string) (SecretDriverFactory, error) {
	driverMu.RLock()
	defer driverMu.RUnlock()
	f, ok := driverFactories[name]
	if !ok {
		names := make([]string, 0, len(driverFactories))
		for n := range driverFactories {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown secrets.driver %q (registered: %v)", name, names)
	}
	return f, nil
}

// defaultDriver is the driver used when a config has no secrets block: the
// same agent→keepass stack the secretcli command tree manages.
const defaultDriver = "agent"

func init() {
	RegisterSecretDriver("agent", agentDriver)
	RegisterSecretDriver("keepass", keepassDriver)
	RegisterSecretDriver("env", envDriverFactory)
	RegisterSecretDriver("exec", execDriverFactory)
	RegisterSecretDriver("none", noneDriverFactory)
}

// agentDriver builds an agent.Client at the app's socket. If no agent is
// running it falls back to a read-only KeepassDriver at the default store
// path, so $(secret:…) resolves whether or not the agent is up.
func agentDriver(cfg *settings.AppConfig, opts *yaml.Node, env Env) (secrets.Driver, error) {
	var o struct {
		Socket string `yaml:"socket"`
	}
	if err := decodeOpts(opts, &o); err != nil {
		return nil, err
	}
	sock := o.Socket
	if sock == "" {
		sock = agent.SocketPath(cfg.AppName())
	}
	client := agent.NewClient(sock)
	if client.IsRunning() {
		return client, nil
	}
	// No agent: fall back to a read-only keepass reader.
	d := secrets.NewKeepassDriver(cfg.File("secrets.kdbx"))
	d.ReadOnly = true
	return d, nil
}

// keepassDriver builds a KeepassDriver. It defaults to a read-only reader at
// cfg.File("secrets.kdbx") — the right posture for a config loader, which
// never writes and need not retain the master key.
func keepassDriver(cfg *settings.AppConfig, opts *yaml.Node, env Env) (secrets.Driver, error) {
	var o struct {
		Path     string `yaml:"path"`
		ReadOnly *bool  `yaml:"readonly"`
	}
	if err := decodeOpts(opts, &o); err != nil {
		return nil, err
	}
	path := o.Path
	if path == "" {
		path = cfg.File("secrets.kdbx")
	} else {
		p, err := expandHome(path)
		if err != nil {
			return nil, fmt.Errorf("secrets.keepass.path: %w", err)
		}
		path = p
	}
	d := secrets.NewKeepassDriver(path)
	d.ReadOnly = o.ReadOnly == nil || *o.ReadOnly // default true for a reader
	return d, nil
}

// envDriverFactory builds a read-only driver backed by the process
// environment: secret:api/token reads $API_TOKEN. It gives $(secret:…) the
// taint/redaction guarantee without a separate store.
func envDriverFactory(_ *settings.AppConfig, _ *yaml.Node, env Env) (secrets.Driver, error) {
	if env == nil {
		env = OSEnv{}
	}
	return envDriver{env: env}, nil
}

type envDriver struct{ env Env }

func (envDriver) Unlock() error { return nil }

func (d envDriver) Get(key string) (*secrets.Secret, error) {
	if v, ok := d.env.Lookup(envKey(key)); ok {
		return &secrets.Secret{Key: key, Value: v}, nil
	}
	return nil, secrets.ErrNotFound
}

func (envDriver) Set(secrets.Secret) error        { return secrets.ErrReadOnly }
func (envDriver) List() ([]secrets.Secret, error) { return nil, nil }
func (envDriver) Delete(string) error             { return secrets.ErrReadOnly }

// envKey maps a path-like secret name to an env var name: uppercased, every
// non-[A-Z0-9] rune replaced with '_' (api/token → API_TOKEN).
func envKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// execDriverFactory builds a read-only driver that shells out to a command
// with the secret name as its final argument and reads the value from stdout —
// covers pass, sops, vault, etc. A non-zero exit is treated as "not found".
func execDriverFactory(_ *settings.AppConfig, opts *yaml.Node, _ Env) (secrets.Driver, error) {
	var o struct {
		Command []string `yaml:"command"`
	}
	if err := decodeOpts(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Command) == 0 {
		return nil, fmt.Errorf("secrets.exec.command is required (e.g. [pass, show])")
	}
	return execDriver{cmd: o.Command}, nil
}

type execDriver struct{ cmd []string }

func (execDriver) Unlock() error { return nil }

func (d execDriver) Get(key string) (*secrets.Secret, error) {
	args := append(append([]string{}, d.cmd[1:]...), key)
	out, err := exec.Command(d.cmd[0], args...).Output()
	if err != nil {
		// A command failure means the backend could not supply the key;
		// surface it as not-found so the loader reports one uniform
		// "unresolved $(secret:…)" error.
		return nil, secrets.ErrNotFound
	}
	return &secrets.Secret{Key: key, Value: strings.TrimRight(string(out), "\r\n")}, nil
}

func (execDriver) Set(secrets.Secret) error        { return secrets.ErrReadOnly }
func (execDriver) List() ([]secrets.Secret, error) { return nil, nil }
func (execDriver) Delete(string) error             { return secrets.ErrReadOnly }

// noneDriverFactory builds a store with no secrets: any $(secret:…) is a fatal
// "not found". For configs that must be secret-free.
func noneDriverFactory(_ *settings.AppConfig, _ *yaml.Node, _ Env) (secrets.Driver, error) {
	return noneDriver{}, nil
}

type noneDriver struct{}

func (noneDriver) Unlock() error                       { return nil }
func (noneDriver) Get(string) (*secrets.Secret, error) { return nil, secrets.ErrNotFound }
func (noneDriver) Set(secrets.Secret) error            { return secrets.ErrReadOnly }
func (noneDriver) List() ([]secrets.Secret, error)     { return nil, nil }
func (noneDriver) Delete(string) error                 { return secrets.ErrReadOnly }

// decodeOpts strictly decodes a driver's option sub-block, tolerating a nil
// node (no options given).
func decodeOpts(opts *yaml.Node, out any) error {
	if opts == nil {
		return nil
	}
	return strictDecode(opts, out)
}

// expandHome resolves a leading ~/ against the user's home directory.
// Templating itself never expands ~ — this is a courtesy for paths the loader
// opens directly.
func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path[1:], "/")), nil
	}
	return path, nil
}
