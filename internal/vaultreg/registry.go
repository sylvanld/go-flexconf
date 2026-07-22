// Package vaultreg implements the vault registry: the layered, operator-owned
// map of vault name → VaultConf (driver + non-secret settings) read from
// ~/.config/flexconf/vaults.yaml or the FLEXCONF_VAULTS file list.
//
// The registry is normatively static: definitions are read literally, no
// $(...) token of any kind is admitted, and credentials are never stored in
// it. It is shared by the flexconf secret: resolver, the agent runtime, and
// the flexcli commands, so it lives in a module-internal package below all of
// them (importing only flexvault for driver construction).
package vaultreg

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sylvanld/go-flexconf/flexvault"
)

// EnvVaults is the environment variable naming the exhaustive, ordered list
// of registry files (OS path-list separated). When set it replaces discovery
// of the well-known file entirely.
const EnvVaults = "FLEXCONF_VAULTS"

// VaultConf is one vault definition: a driver name plus that driver's own
// non-secret settings, handed verbatim to Configure. Credentials are never
// part of a definition.
type VaultConf struct {
	Driver string         // registered flexvault driver name
	Config map[string]any // driver settings (path, readonly, url, …)

	// Source is the registry file that defined this entry (the last one to
	// set it — whole-entry replacement); Overrode lists earlier definitions
	// it replaced.
	Source   string
	Overrode []string
}

// FileStatus records one consulted registry file for provenance output.
type FileStatus struct {
	Path    string
	Exists  bool
	FromEnv bool // named in FLEXCONF_VAULTS (vs the well-known file)
	Err     error
}

// Registry is the effective, merged vault registry.
type Registry struct {
	Default       string // name of the default vault ("" if unset)
	DefaultSource string // file that set Default
	Vaults        map[string]VaultConf
	Files         []FileStatus // consulted files, in order
}

// WellKnownFile returns the well-known registry path,
// $XDG_CONFIG_HOME/flexconf/vaults.yaml (default ~/.config/flexconf/vaults.yaml).
func WellKnownFile() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "flexconf", "vaults.yaml")
}

// fileList returns the ordered registry files: the FLEXCONF_VAULTS list when
// set (exhaustive — replaces discovery), else the single well-known file.
func fileList() (paths []string, fromEnv bool) {
	if list, ok := os.LookupEnv(EnvVaults); ok {
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				paths = append(paths, p)
			}
		}
		return paths, true
	}
	if wk := WellKnownFile(); wk != "" {
		paths = append(paths, wk)
	}
	return paths, false
}

// registryFile is the on-disk YAML shape of one registry file.
type registryFile struct {
	Default string                    `yaml:"default"`
	Vaults  map[string]map[string]any `yaml:"vaults"`
}

// Load assembles the effective registry from the environment-derived file
// list, applying later-file-wins whole-entry replacement. A missing file is
// recorded (and skipped); a present file that cannot be parsed, or one whose
// definitions carry $(...) tokens, fails loudly.
func Load() (*Registry, error) {
	reg := &Registry{Vaults: map[string]VaultConf{}}
	paths, fromEnv := fileList()
	for _, path := range paths {
		status := FileStatus{Path: path, FromEnv: fromEnv}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			reg.Files = append(reg.Files, status)
			continue
		}
		if err != nil {
			status.Err = err
			reg.Files = append(reg.Files, status)
			return nil, fmt.Errorf("vaultreg: reading %s: %w", path, err)
		}
		status.Exists = true
		reg.Files = append(reg.Files, status)

		var rf registryFile
		if err := yaml.Unmarshal(data, &rf); err != nil {
			return nil, fmt.Errorf("vaultreg: parsing %s: %w", path, err)
		}
		if err := mergeFile(reg, &rf, path); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// mergeFile applies one registry file: vaults map deep-merges by name with
// whole-entry VaultConf replacement; the last default: wins.
func mergeFile(reg *Registry, rf *registryFile, path string) error {
	if rf.Default != "" {
		reg.Default, reg.DefaultSource = rf.Default, path
	}
	for name, raw := range rf.Vaults {
		conf := VaultConf{Config: map[string]any{}, Source: path}
		for key, value := range raw {
			if err := checkStatic(value, path, name, key); err != nil {
				return err
			}
			if key == "driver" {
				s, ok := value.(string)
				if !ok {
					return fmt.Errorf("vaultreg: %s: vault %q: driver must be a string", path, name)
				}
				conf.Driver = s
				continue
			}
			conf.Config[key] = value
		}
		if conf.Driver == "" {
			return fmt.Errorf("vaultreg: %s: vault %q: driver is required", path, name)
		}
		normalizePaths(&conf, path)
		if prev, ok := reg.Vaults[name]; ok {
			conf.Overrode = append(append([]string{}, prev.Overrode...), prev.Source)
		}
		reg.Vaults[name] = conf
	}
	return nil
}

// checkStatic enforces the static-definitions rule: no $(...) token anywhere
// in a VaultConf value.
func checkStatic(value any, path, vault, key string) error {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "$(") {
			return fmt.Errorf("vaultreg: %s: vault %q key %q: definitions are static — no $(...) tokens allowed",
				path, vault, key)
		}
	case map[string]any:
		for k, sub := range v {
			if err := checkStatic(sub, path, vault, key+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for _, sub := range v {
			if err := checkStatic(sub, path, vault, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// normalizePaths applies the central path rules to a VaultConf's "path" key:
// a leading ~ expands to the user's home; a relative path resolves against
// the directory of the registry file that defined the entry.
func normalizePaths(conf *VaultConf, sourceFile string) {
	raw, ok := conf.Config["path"].(string)
	if !ok || raw == "" {
		return
	}
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(raw, "~"), "/"))
		}
	} else if !filepath.IsAbs(raw) {
		raw = filepath.Join(filepath.Dir(sourceFile), raw)
	}
	conf.Config["path"] = raw
}

// Names returns the sorted vault names in the effective registry.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.Vaults))
	for n := range r.Vaults {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Resolve returns the named vault's VaultConf; with name == "" it resolves
// the default vault, failing loudly when none is configured.
func (r *Registry) Resolve(name string) (string, VaultConf, error) {
	if name == "" {
		if r.Default == "" {
			return "", VaultConf{}, fmt.Errorf("vaultreg: no vault named and no default vault configured (set default: in vaults.yaml)")
		}
		name = r.Default
	}
	conf, ok := r.Vaults[name]
	if !ok {
		return "", VaultConf{}, fmt.Errorf("vaultreg: unknown vault %q (known: %s)", name, strings.Join(r.Names(), ", "))
	}
	return name, conf, nil
}

// BuildDriver constructs and Configures the vault's registered flexvault
// driver from its (already normalized) config.
func (c VaultConf) BuildDriver() (flexvault.VaultDriver, error) {
	drv, err := flexvault.New(c.Driver)
	if err != nil {
		return nil, err
	}
	if err := drv.Configure(flexvault.MapDecoder(c.Config)); err != nil {
		return nil, fmt.Errorf("vaultreg: configuring driver %q: %w", c.Driver, err)
	}
	return drv, nil
}

// VaultID derives the agent identity for a named, resolved vault:
// "<name>-<fp>" where fp is the first 8 hex chars of SHA-256 over the
// canonical (keys-sorted YAML) serialization of the resolved non-secret
// VaultConf.
func VaultID(name string, conf VaultConf) string {
	return name + "-" + Fingerprint(conf)
}

// Fingerprint computes the 8-hex-char canonical-config fingerprint of a
// VaultConf (non-secret material only).
func Fingerprint(conf VaultConf) string {
	canonical := map[string]any{"driver": conf.Driver}
	for k, v := range conf.Config {
		canonical[k] = v
	}
	// yaml.Marshal sorts map keys, giving a canonical serialization.
	data, err := yaml.Marshal(canonical)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", canonical))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:4])
}

// ParseRef splits a secret: token path into its optional vault name and the
// namespace/key address: "[vault:]namespace/key". A ":" before the first "/"
// separates the vault name; the name must not contain "/" or ":".
func ParseRef(path string) (vault, addr string, err error) {
	slash := strings.IndexByte(path, '/')
	colon := strings.IndexByte(path, ':')
	if colon >= 0 && (slash < 0 || colon < slash) {
		vault, addr = path[:colon], path[colon+1:]
		if vault == "" || strings.ContainsAny(addr, ":") {
			return "", "", fmt.Errorf("vaultreg: invalid secret reference %q (want [vault:]namespace/key)", path)
		}
		return vault, addr, nil
	}
	if strings.ContainsAny(path, ":") {
		return "", "", fmt.Errorf("vaultreg: invalid secret reference %q (want [vault:]namespace/key)", path)
	}
	return "", path, nil
}
