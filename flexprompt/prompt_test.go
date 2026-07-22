package flexprompt

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMapPrompter(t *testing.T) {
	p := NewMapPrompter(map[string]string{"password": "s3cret"})

	t.Run("resolves required", func(t *testing.T) {
		answers, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "password", Secret: true}})
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		if answers["password"] != "s3cret" {
			t.Fatalf("answers = %v, want password=s3cret", answers)
		}
	})

	t.Run("missing required fails with ErrPromptUnavailable", func(t *testing.T) {
		_, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "token"}})
		if !errors.Is(err, ErrPromptUnavailable) {
			t.Fatalf("err = %v, want ErrPromptUnavailable", err)
		}
	})

	t.Run("missing optional omitted", func(t *testing.T) {
		answers, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "token", Optional: true}})
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		if _, ok := answers["token"]; ok {
			t.Fatal("optional missing key should be omitted")
		}
	})

	t.Run("duplicate ID is an error", func(t *testing.T) {
		_, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "a"}, {ID: "a"}})
		if err == nil {
			t.Fatal("want error for duplicate ID")
		}
	})

	t.Run("empty ID is an error", func(t *testing.T) {
		_, err := p.Dispatch(context.Background(), []PromptRequest{{ID: ""}})
		if err == nil {
			t.Fatal("want error for empty ID")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Dispatch(ctx, []PromptRequest{{ID: "password"}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want wrapped context.Canceled", err)
		}
	})
}

func TestEnvPrompter(t *testing.T) {
	t.Setenv("FLEXTEST_PASSWORD", "pw")
	t.Setenv("FLEXTEST_KEYFILE_PASSPHRASE", "kp")
	p := NewEnvPrompter("FLEXTEST_")

	answers, err := p.Dispatch(context.Background(), []PromptRequest{
		{ID: "password", Secret: true},
		{ID: "keyfile-passphrase", Secret: true}, // dashes map to underscores
		{ID: "absent", Optional: true},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if answers["password"] != "pw" || answers["keyfile-passphrase"] != "kp" {
		t.Fatalf("answers = %v", answers)
	}
	if _, ok := answers["absent"]; ok {
		t.Fatal("absent optional should be omitted")
	}

	_, err = p.Dispatch(context.Background(), []PromptRequest{{ID: "absent"}})
	if !errors.Is(err, ErrPromptUnavailable) {
		t.Fatalf("err = %v, want ErrPromptUnavailable", err)
	}
}

func TestSingleton(t *testing.T) {
	t.Cleanup(func() { SetPrompter(nil) })

	t.Run("default fails required with ErrNoPrompter", func(t *testing.T) {
		SetPrompter(nil)
		_, err := GetPrompter().Dispatch(context.Background(), []PromptRequest{{ID: "password"}})
		if !errors.Is(err, ErrNoPrompter) {
			t.Fatalf("err = %v, want ErrNoPrompter", err)
		}
	})

	t.Run("default succeeds with only optional requests", func(t *testing.T) {
		SetPrompter(nil)
		answers, err := GetPrompter().Dispatch(context.Background(), []PromptRequest{{ID: "x", Optional: true}})
		if err != nil || len(answers) != 0 {
			t.Fatalf("answers, err = %v, %v; want empty, nil", answers, err)
		}
	})

	t.Run("set and get round-trips", func(t *testing.T) {
		p := NewMapPrompter(map[string]string{"a": "1"})
		SetPrompter(p)
		answers, err := GetPrompter().Dispatch(context.Background(), []PromptRequest{{ID: "a"}})
		if err != nil || answers["a"] != "1" {
			t.Fatalf("answers, err = %v, %v", answers, err)
		}
	})

	t.Run("set nil resets to default", func(t *testing.T) {
		SetPrompter(NewMapPrompter(nil))
		SetPrompter(nil)
		_, err := GetPrompter().Dispatch(context.Background(), []PromptRequest{{ID: "a"}})
		if !errors.Is(err, ErrNoPrompter) {
			t.Fatalf("err = %v, want ErrNoPrompter", err)
		}
	})
}

func TestPrompterFunc(t *testing.T) {
	p := PrompterFunc(func(_ context.Context, reqs []PromptRequest) (map[string]string, error) {
		return map[string]string{"x": "y"}, nil
	})
	answers, err := p.Dispatch(context.Background(), nil)
	if err != nil || answers["x"] != "y" {
		t.Fatalf("answers, err = %v, %v", answers, err)
	}
}

// cliInput builds a temp file pre-filled with input lines to act as the CLI
// prompter's (non-terminal) input stream.
func cliInput(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestCLIPrompter(t *testing.T) {
	t.Run("reads lines and applies default", func(t *testing.T) {
		var out strings.Builder
		p := NewCLIPrompter(WithCLIStreams(cliInput(t, "alice\n\n"), &out))
		answers, err := p.Dispatch(context.Background(), []PromptRequest{
			{ID: "user", Label: "User"},
			{ID: "region", Label: "Region", Default: "eu"},
		})
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		if answers["user"] != "alice" || answers["region"] != "eu" {
			t.Fatalf("answers = %v", answers)
		}
		if !strings.Contains(out.String(), "Region [eu]: ") {
			t.Fatalf("output %q should show the default", out.String())
		}
	})

	t.Run("confirm mismatch fails", func(t *testing.T) {
		var out strings.Builder
		p := NewCLIPrompter(WithCLIStreams(cliInput(t, "one\ntwo\n"), &out))
		_, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "pw", Confirm: true}})
		if err == nil {
			t.Fatal("want mismatch error")
		}
	})

	t.Run("confirm match succeeds", func(t *testing.T) {
		var out strings.Builder
		p := NewCLIPrompter(WithCLIStreams(cliInput(t, "same\nsame\n"), &out))
		answers, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "pw", Confirm: true}})
		if err != nil || answers["pw"] != "same" {
			t.Fatalf("answers, err = %v, %v", answers, err)
		}
	})

	t.Run("EOF on required input is cancellation", func(t *testing.T) {
		var out strings.Builder
		p := NewCLIPrompter(WithCLIStreams(cliInput(t, ""), &out))
		_, err := p.Dispatch(context.Background(), []PromptRequest{{ID: "pw"}})
		if !errors.Is(err, ErrPromptCancelled) {
			t.Fatalf("err = %v, want ErrPromptCancelled", err)
		}
	})

	t.Run("secret default is ignored", func(t *testing.T) {
		var out strings.Builder
		p := NewCLIPrompter(WithCLIStreams(cliInput(t, "typed\n"), &out))
		answers, err := p.Dispatch(context.Background(), []PromptRequest{
			{ID: "pw", Secret: true, Default: "should-not-show"},
		})
		if err != nil || answers["pw"] != "typed" {
			t.Fatalf("answers, err = %v, %v", answers, err)
		}
		if strings.Contains(out.String(), "should-not-show") {
			t.Fatal("secret default must not be displayed")
		}
	})
}
