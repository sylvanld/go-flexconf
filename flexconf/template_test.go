package flexconf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// env returns a WithEnv source backed by a map.
func env(m map[string]string) Option {
	return WithEnv(func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	})
}

func TestEnvTokens(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
host: $(env:HOST)
port: $(env:PORT)
url: https://$(env:HOST):$(env:PORT)/api
`)
	var cfg struct {
		Host string `flexconf:"host"`
		Port int    `flexconf:"port"`
		URL  string `flexconf:"url"`
	}
	l := New(dir).With(env(map[string]string{"HOST": "example.com", "PORT": "8080"}))
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Whole-value token typed by content ("8080" → int); mixed scalar is a string.
	if cfg.Host != "example.com" || cfg.Port != 8080 || cfg.URL != "https://example.com:8080/api" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestEnvMissingIsHardError(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "host: $(env:NOPE)\n")
	var cfg struct {
		Host string `flexconf:"host"`
	}
	err := New(dir).With(env(nil)).Load("config.yaml", &cfg)
	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("err = %v, want ErrEnvNotSet", err)
	}
	if !stringsContains(err.Error(), "host") {
		t.Fatalf("err %q should name the key path", err)
	}
}

func TestWholeValueTyping(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "flag: $(env:FLAG)\ntimeout: $(env:T)\nblob: {v: $(env:PORT)}\n")
	var cfg struct {
		Flag    bool           `flexconf:"flag"`
		Timeout time.Duration  `flexconf:"timeout"`
		Blob    map[string]any `flexconf:"blob"`
	}
	l := New(dir).With(env(map[string]string{"FLAG": "true", "T": "30s", "PORT": "8080"}))
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Flag || cfg.Timeout != 30*time.Second {
		t.Fatalf("cfg = %+v", cfg)
	}
	// Untagged substituted scalar infers from resolved content in any targets.
	if cfg.Blob["v"] != int64(8080) {
		t.Fatalf("Blob.v = %#v, want int64(8080)", cfg.Blob["v"])
	}
}

func TestEscaping(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
price: "$$(not-a-token)"
cost: $5
double: a$$b
`)
	var cfg struct {
		Price  string `flexconf:"price"`
		Cost   string `flexconf:"cost"`
		Double string `flexconf:"double"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Price != "$(not-a-token)" {
		t.Fatalf("Price = %q, want $(not-a-token)", cfg.Price)
	}
	if cfg.Cost != "$5" || cfg.Double != "a$$b" {
		t.Fatalf("cfg = %+v ($ and $$ elsewhere stay literal)", cfg)
	}
}

func TestBadTokens(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"v: $(env:UNTERMINATED\n", "v: $(no-colon)\n", "v: $(BAD:x)\n"} {
		writeConfig(t, dir, "config.yaml", bad)
		var cfg struct {
			V string `flexconf:"v"`
		}
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrBadToken) {
			t.Fatalf("Load(%q) err = %v, want ErrBadToken", bad, err)
		}
	}
}

func TestNoNesting(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "v: $(env:OUTER)\n")
	var cfg struct {
		V string `flexconf:"v"`
	}
	// A resolved value is inert: a $(...) inside it stays literal.
	l := New(dir).With(env(map[string]string{"OUTER": "$(env:INNER)"}))
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.V != "$(env:INNER)" {
		t.Fatalf("V = %q, want literal inner token", cfg.V)
	}
}

func TestKeysNeverTemplated(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "$(env:X): v\n")
	var cfg struct {
		Blob map[string]string `flexconf:",inline"`
	}
	_ = cfg
	var cfg2 struct {
		M map[string]string `flexconf:"m"`
	}
	writeConfig(t, dir, "config.yaml", "m: {$(env:X): v}\n")
	if err := New(dir).Load("config.yaml", &cfg2); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg2.M["$(env:X)"] != "v" {
		t.Fatalf("M = %v, want literal key", cfg2.M)
	}
}

func TestFileToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("PEM-DATA\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, "config.yaml", "cert: $(file:cert.pem)\n")
	var cfg struct {
		Cert string `flexconf:"cert"`
	}
	if err := New(dir).Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Verbatim contents: trailing newline preserved.
	if cfg.Cert != "PEM-DATA\nline2\n" {
		t.Fatalf("Cert = %q", cfg.Cert)
	}

	writeConfig(t, dir, "config.yaml", "cert: $(file:missing.pem)\n")
	if err := New(dir).Load("config.yaml", &cfg); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("err = %v, want ErrFileNotFound", err)
	}
}

func TestCustomResolvers(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "v: $(up:hello)\n")
	var cfg struct {
		V string `flexconf:"v"`
	}

	t.Run("unknown scheme", func(t *testing.T) {
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrUnknownScheme) {
			t.Fatalf("err = %v, want ErrUnknownScheme", err)
		}
	})

	t.Run("loader-scoped resolver", func(t *testing.T) {
		l := New(dir).With(WithResolver(resolverFunc("up", func(p string) (string, error) {
			return "UP:" + p, nil
		})))
		if err := l.Load("config.yaml", &cfg); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.V != "UP:hello" {
			t.Fatalf("V = %q", cfg.V)
		}
	})

	t.Run("global registration", func(t *testing.T) {
		RegisterResolver(resolverFunc("glob", func(p string) (string, error) { return "G:" + p, nil }))
		writeConfig(t, dir, "config.yaml", "v: $(glob:x)\n")
		if err := New(dir).Load("config.yaml", &cfg); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.V != "G:x" {
			t.Fatalf("V = %q", cfg.V)
		}
	})

	t.Run("duplicate global registration panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("want panic")
			}
		}()
		RegisterResolver(resolverFunc("glob", func(p string) (string, error) { return "", nil }))
	})

	t.Run("default scheme registration panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("want panic")
			}
		}()
		RegisterResolver(resolverFunc("env", func(p string) (string, error) { return "", nil }))
	})
}

// resolverFunc builds a Resolver from a function.
func resolverFunc(scheme string, fn func(string) (string, error)) Resolver {
	return testResolver{scheme: scheme, fn: fn}
}

type testResolver struct {
	scheme string
	fn     func(string) (string, error)
}

func (r testResolver) Scheme() string { return r.scheme }
func (r testResolver) Resolve(_ context.Context, path string) (string, error) {
	return r.fn(path)
}

func TestWithResolversReplacement(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "v: $(env:X)\n")
	var cfg struct {
		V string `flexconf:"v"`
	}

	t.Run("replaced set drops defaults", func(t *testing.T) {
		l := New(dir).With(WithResolvers(resolverFunc("my", func(p string) (string, error) {
			return "mine", nil
		})))
		err := l.Load("config.yaml", &cfg)
		if !errors.Is(err, ErrUnknownScheme) {
			t.Fatalf("err = %v, want ErrUnknownScheme (env not in replaced set)", err)
		}
		writeConfig(t, dir, "config.yaml", "v: $(my:x)\n")
		if err := l.Load("config.yaml", &cfg); err != nil || cfg.V != "mine" {
			t.Fatalf("cfg, err = %+v, %v", cfg, err)
		}
	})

	t.Run("WithResolver composes on top", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "v: $(extra:x)\n")
		l := New(dir).With(WithResolvers(), WithResolver(resolverFunc("extra", func(p string) (string, error) {
			return "composed", nil
		})))
		if err := l.Load("config.yaml", &cfg); err != nil || cfg.V != "composed" {
			t.Fatalf("cfg, err = %+v, %v", cfg, err)
		}
	})
}

func TestStaticLoader(t *testing.T) {
	dir := t.TempDir()
	var cfg struct {
		V string `flexconf:"v"`
	}
	static := New(dir).With(WithResolvers())

	t.Run("plain literals pass through", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "v: plain\n")
		if err := static.Load("config.yaml", &cfg); err != nil || cfg.V != "plain" {
			t.Fatalf("cfg, err = %+v, %v", cfg, err)
		}
	})

	t.Run("any token is an error", func(t *testing.T) {
		for _, tok := range []string{"v: $(env:X)\n", "v: $(config:other.yaml)\n"} {
			writeConfig(t, dir, "config.yaml", tok)
			err := static.Load("config.yaml", &cfg)
			if !errors.Is(err, ErrUnknownScheme) {
				t.Fatalf("Load(%q) err = %v, want ErrUnknownScheme", tok, err)
			}
		}
	})
}

func TestIncludes(t *testing.T) {
	dir := t.TempDir()
	var cfg struct {
		Agents map[string]any `flexconf:"agents"`
		Items  []any          `flexconf:"items"`
	}

	t.Run("whole-value splice", func(t *testing.T) {
		writeConfig(t, dir, "agents.yaml", "alpha: {model: a}\nbeta: {model: b}\n")
		writeConfig(t, dir, "base.yaml", "kind: base\n")
		writeConfig(t, dir, "config.yaml", "agents: $(config:agents.yaml)\nitems:\n  - $(config:base.yaml)\n")
		if err := New(dir).Load("config.yaml", &cfg); err != nil {
			t.Fatalf("Load: %v", err)
		}
		alpha, ok := cfg.Agents["alpha"].(map[string]any)
		if !ok || alpha["model"] != "a" {
			t.Fatalf("Agents = %#v", cfg.Agents)
		}
		item, ok := cfg.Items[0].(map[string]any)
		if !ok || item["kind"] != "base" {
			t.Fatalf("Items = %#v", cfg.Items)
		}
	})

	t.Run("nested includes with tokens inside", func(t *testing.T) {
		writeConfig(t, dir, "inner.yaml", "value: $(env:V)\n")
		writeConfig(t, dir, "outer.yaml", "inner: $(config:inner.yaml)\n")
		writeConfig(t, dir, "config.yaml", "agents: $(config:outer.yaml)\nitems: []\n")
		l := New(dir).With(env(map[string]string{"V": "resolved"}))
		if err := l.Load("config.yaml", &cfg); err != nil {
			t.Fatalf("Load: %v", err)
		}
		inner := cfg.Agents["inner"].(map[string]any)
		if inner["value"] != "resolved" {
			t.Fatalf("tokens inside included files must resolve: %#v", inner)
		}
	})

	t.Run("embedded include is an error", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "agents: prefix-$(config:agents.yaml)\nitems: []\n")
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrIncludeEmbedded) {
			t.Fatalf("err = %v, want ErrIncludeEmbedded", err)
		}
	})

	t.Run("bad extension", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "agents: $(config:notes.txt)\nitems: []\n")
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrIncludeExtension) {
			t.Fatalf("err = %v, want ErrIncludeExtension", err)
		}
	})

	t.Run("escape is contained", func(t *testing.T) {
		writeConfig(t, dir, "config.yaml", "agents: $(config:../outside.yaml)\nitems: []\n")
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrIncludeEscape) {
			t.Fatalf("err = %v, want ErrIncludeEscape", err)
		}
	})

	t.Run("cycle detected with chain", func(t *testing.T) {
		writeConfig(t, dir, "a.yaml", "x: $(config:b.yaml)\n")
		writeConfig(t, dir, "b.yaml", "y: $(config:a.yaml)\n")
		writeConfig(t, dir, "config.yaml", "agents: $(config:a.yaml)\nitems: []\n")
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrIncludeCycle) {
			t.Fatalf("err = %v, want ErrIncludeCycle", err)
		}
		if !stringsContains(err.Error(), "a.yaml") || !stringsContains(err.Error(), "b.yaml") {
			t.Fatalf("err %q should name the chain", err)
		}
	})

	t.Run("depth cap", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			writeConfig(t, dir, fmt.Sprintf("d%d.yaml", i), fmt.Sprintf("next: $(config:d%d.yaml)\n", i+1))
		}
		writeConfig(t, dir, "d20.yaml", "end: true\n")
		writeConfig(t, dir, "config.yaml", "agents: $(config:d0.yaml)\nitems: []\n")
		err := New(dir).Load("config.yaml", &cfg)
		if !errors.Is(err, ErrIncludeTooDeep) {
			t.Fatalf("err = %v, want ErrIncludeTooDeep", err)
		}
	})
}

func TestSecretOriginRedaction(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "port: $(secret:ns/port)\n")
	var cfg struct {
		Port int `flexconf:"port"`
	}
	// A loader-scoped secret resolver stands in for the vault-backed one.
	l := New(dir).With(WithResolver(resolverFunc("secret", func(p string) (string, error) {
		return "not-a-number-and-very-secret", nil
	})))
	err := l.Load("config.yaml", &cfg)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
	if stringsContains(err.Error(), "very-secret") {
		t.Fatalf("err %q leaks a secret-origin value", err)
	}
	if !stringsContains(err.Error(), "port") {
		t.Fatalf("err %q should still name the key path", err)
	}
}

func TestLayeredTokenResolution(t *testing.T) {
	base, override := t.TempDir(), t.TempDir()
	writeConfig(t, base, "config.yaml", "v: base\n")
	writeConfig(t, override, "config.yaml", "v: $(env:X)\n")
	var cfg struct {
		V string `flexconf:"v"`
	}
	// A value introduced by a higher-precedence layer may itself contain a
	// token — resolution runs on the merged tree.
	l := New(base, override).With(env(map[string]string{"X": "from-env"}))
	if err := l.Load("config.yaml", &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.V != "from-env" {
		t.Fatalf("V = %q", cfg.V)
	}
}
