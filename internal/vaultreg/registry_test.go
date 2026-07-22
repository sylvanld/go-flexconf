package vaultreg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWellKnown(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	os.Unsetenv(EnvVaults)
	if err := os.MkdirAll(filepath.Join(cfgHome, "flexconf"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfgHome, "flexconf"), "vaults.yaml", `
default: personal
vaults:
  personal:
    driver: keepass
    path: /vaults/personal.kdbx
    readonly: false
`)
	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Default != "personal" || len(reg.Vaults) != 1 {
		t.Fatalf("reg = %+v", reg)
	}
	name, conf, err := reg.Resolve("")
	if err != nil || name != "personal" || conf.Driver != "keepass" {
		t.Fatalf("Resolve = %q, %+v, %v", name, conf, err)
	}
}

func TestMissingWellKnownIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv(EnvVaults)
	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Vaults) != 0 || reg.Default != "" {
		t.Fatalf("reg = %+v, want empty", reg)
	}
	if len(reg.Files) != 1 || reg.Files[0].Exists {
		t.Fatalf("Files = %+v", reg.Files)
	}
}

func TestEnvListLayering(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yaml", `
default: personal
vaults:
  personal: {driver: keepass, path: /a.kdbx}
  work:     {driver: keepass, path: /w1.kdbx, readonly: true}
`)
	override := writeFile(t, dir, "override.yaml", `
default: work
vaults:
  work: {driver: keepass, path: /w2.kdbx}
`)
	t.Setenv(EnvVaults, base+string(os.PathListSeparator)+override)

	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Last default: wins.
	if reg.Default != "work" || reg.DefaultSource != override {
		t.Fatalf("Default = %q from %q", reg.Default, reg.DefaultSource)
	}
	// Whole-entry replacement: work loses its readonly key entirely.
	work := reg.Vaults["work"]
	if work.Config["path"] != "/w2.kdbx" {
		t.Fatalf("work = %+v", work)
	}
	if _, kept := work.Config["readonly"]; kept {
		t.Fatal("whole-entry replacement must not deep-merge keys")
	}
	if len(work.Overrode) != 1 || work.Overrode[0] != base {
		t.Fatalf("Overrode = %v", work.Overrode)
	}
	// Entries only in the base file are retained.
	if reg.Vaults["personal"].Config["path"] != "/a.kdbx" {
		t.Fatalf("personal = %+v", reg.Vaults["personal"])
	}
}

func TestEnvListReplacesDiscovery(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	os.MkdirAll(filepath.Join(cfgHome, "flexconf"), 0o700)
	writeFile(t, filepath.Join(cfgHome, "flexconf"), "vaults.yaml", "default: wk\nvaults: {wk: {driver: keepass, path: /wk.kdbx}}\n")

	dir := t.TempDir()
	only := writeFile(t, dir, "only.yaml", "vaults: {solo: {driver: keepass, path: /solo.kdbx}}\n")
	t.Setenv(EnvVaults, only)

	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Vaults["wk"]; ok {
		t.Fatal("well-known file must be ignored when FLEXCONF_VAULTS is set")
	}
	if _, ok := reg.Vaults["solo"]; !ok {
		t.Fatalf("reg = %+v", reg)
	}
}

func TestStaticRule(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.yaml", "vaults: {v: {driver: keepass, path: $(env:HOME)/x.kdbx}}\n")
	t.Setenv(EnvVaults, bad)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "static") {
		t.Fatalf("err = %v, want static-rule violation", err)
	}
}

func TestPathNormalization(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "reg.yaml", `
vaults:
  rel:  {driver: keepass, path: sub/rel.kdbx}
  home: {driver: keepass, path: ~/secret.kdbx}
  abs:  {driver: keepass, path: /abs.kdbx}
`)
	t.Setenv(EnvVaults, file)
	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reg.Vaults["rel"].Config["path"]; got != filepath.Join(dir, "sub/rel.kdbx") {
		t.Fatalf("rel path = %v (must resolve against the defining file's dir)", got)
	}
	home, _ := os.UserHomeDir()
	if got := reg.Vaults["home"].Config["path"]; got != filepath.Join(home, "secret.kdbx") {
		t.Fatalf("home path = %v", got)
	}
	if got := reg.Vaults["abs"].Config["path"]; got != "/abs.kdbx" {
		t.Fatalf("abs path = %v", got)
	}
}

func TestResolveErrors(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "reg.yaml", "vaults: {a: {driver: keepass, path: /a.kdbx}}\n")
	t.Setenv(EnvVaults, file)
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// No default configured: unqualified resolution fails loudly.
	if _, _, err := reg.Resolve(""); err == nil || !strings.Contains(err.Error(), "no default vault") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := reg.Resolve("nope"); err == nil || !strings.Contains(err.Error(), `unknown vault "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingDriverFails(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "reg.yaml", "vaults: {v: {path: /x.kdbx}}\n")
	t.Setenv(EnvVaults, file)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "driver is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestVaultID(t *testing.T) {
	conf := VaultConf{Driver: "keepass", Config: map[string]any{"path": "/a.kdbx"}}
	id := VaultID("work", conf)
	if !strings.HasPrefix(id, "work-") || len(id) != len("work-")+8 {
		t.Fatalf("VaultID = %q", id)
	}
	// Deterministic; changes with config.
	if VaultID("work", conf) != id {
		t.Fatal("VaultID must be deterministic")
	}
	other := VaultConf{Driver: "keepass", Config: map[string]any{"path": "/b.kdbx"}}
	if VaultID("work", other) == id {
		t.Fatal("different resolved config must give a different VaultID")
	}
	// Same config, different name → different ID, same fingerprint.
	if VaultID("home", conf) == id || Fingerprint(conf) != strings.TrimPrefix(id, "work-") {
		t.Fatal("name is part of the identity; fingerprint is config-only")
	}
}

func TestParseRef(t *testing.T) {
	cases := []struct {
		in, vault, addr string
		wantErr         bool
	}{
		{"artifactory/token", "", "artifactory/token", false},
		{"work:deploy/key", "work", "deploy/key", false},
		{"personal:github/pat", "personal", "github/pat", false},
		{":ns/key", "", "", true},
		{"a:b:c/d", "", "", true},
		{"ns/key:extra", "", "", true},
	}
	for _, c := range cases {
		vault, addr, err := ParseRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ParseRef(%q) should fail", c.in)
			}
			continue
		}
		if err != nil || vault != c.vault || addr != c.addr {
			t.Fatalf("ParseRef(%q) = %q, %q, %v", c.in, vault, addr, err)
		}
	}
}
