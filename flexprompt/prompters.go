package flexprompt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// NewMapPrompter returns a non-interactive Prompter that resolves each
// req.ID from the given map. A required ID absent from the map yields
// ErrPromptUnavailable; an optional one is omitted from the answers.
func NewMapPrompter(values map[string]string) Prompter {
	return PrompterFunc(func(ctx context.Context, reqs []PromptRequest) (map[string]string, error) {
		if err := validateRequests(reqs); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("flexprompt: %w", err)
		}
		answers := make(map[string]string, len(reqs))
		for _, r := range reqs {
			v, ok := values[r.ID]
			if !ok {
				if r.Optional {
					continue
				}
				return nil, fmt.Errorf("flexprompt: required prompt %q: %w", r.ID, ErrPromptUnavailable)
			}
			answers[r.ID] = v
		}
		return answers, nil
	})
}

// NewEnvPrompter returns a non-interactive Prompter that resolves each req.ID
// from the environment variable prefix+ID (the ID is uppercased and dashes
// become underscores, e.g. prefix "FLEXCONF_" + ID "keyfile-passphrase" reads
// FLEXCONF_KEYFILE_PASSPHRASE).
func NewEnvPrompter(prefix string) Prompter {
	return PrompterFunc(func(ctx context.Context, reqs []PromptRequest) (map[string]string, error) {
		if err := validateRequests(reqs); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("flexprompt: %w", err)
		}
		answers := make(map[string]string, len(reqs))
		for _, r := range reqs {
			name := prefix + strings.ToUpper(strings.ReplaceAll(r.ID, "-", "_"))
			v, ok := os.LookupEnv(name)
			if !ok {
				if r.Optional {
					continue
				}
				return nil, fmt.Errorf("flexprompt: required prompt %q (env %s): %w", r.ID, name, ErrPromptUnavailable)
			}
			answers[r.ID] = v
		}
		return answers, nil
	})
}

// cliPrompter prompts on a terminal, masking secret input.
type cliPrompter struct {
	in  *os.File // terminal input (masked reads need a real fd)
	out io.Writer
}

// CLIOption tunes a CLI prompter.
type CLIOption func(*cliPrompter)

// WithCLIStreams overrides the CLI prompter's input file and output writer
// (defaults: os.Stdin / os.Stderr). Masked input requires in to be a terminal;
// on a non-terminal input the prompter reads lines without masking.
func WithCLIStreams(in *os.File, out io.Writer) CLIOption {
	return func(p *cliPrompter) { p.in, p.out = in, out }
}

// NewCLIPrompter returns an interactive Prompter that asks each request on the
// terminal, one after another (a single Dispatch is still one interaction).
// Secret input is masked; Confirm requests are read twice and must match.
func NewCLIPrompter(opts ...CLIOption) Prompter {
	p := &cliPrompter{in: os.Stdin, out: os.Stderr}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *cliPrompter) Dispatch(ctx context.Context, reqs []PromptRequest) (map[string]string, error) {
	if err := validateRequests(reqs); err != nil {
		return nil, err
	}
	answers := make(map[string]string, len(reqs))
	reader := bufio.NewReader(p.in)
	for _, r := range reqs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("flexprompt: %w", err)
		}
		v, err := p.ask(reader, r)
		if err != nil {
			return nil, err
		}
		if v == "" && r.Optional {
			continue
		}
		if v == "" && !r.Optional {
			return nil, fmt.Errorf("flexprompt: required prompt %q: %w", r.ID, ErrPromptUnavailable)
		}
		answers[r.ID] = v
	}
	return answers, nil
}

func (p *cliPrompter) ask(reader *bufio.Reader, r PromptRequest) (string, error) {
	label := r.Label
	if label == "" {
		label = r.ID
	}
	// Default is ignored for secret input (documented caller contract).
	if r.Default != "" && !r.Secret {
		label += " [" + r.Default + "]"
	}
	v, err := p.read(reader, label+": ", r.Secret)
	if err != nil {
		return "", err
	}
	if v == "" && r.Default != "" && !r.Secret {
		v = r.Default
	}
	if r.Confirm && v != "" {
		again, err := p.read(reader, "Confirm "+label+": ", r.Secret)
		if err != nil {
			return "", err
		}
		if again != v {
			return "", fmt.Errorf("flexprompt: entries for %q do not match", r.ID)
		}
	}
	return v, nil
}

func (p *cliPrompter) read(reader *bufio.Reader, label string, secret bool) (string, error) {
	fmt.Fprint(p.out, label)
	fd := int(p.in.Fd())
	if secret && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(p.out)
		if err != nil {
			return "", fmt.Errorf("flexprompt: reading input: %w", err)
		}
		return string(b), nil
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("flexprompt: reading input: %w", err)
	}
	if err != nil && line == "" {
		return "", fmt.Errorf("flexprompt: input closed: %w", ErrPromptCancelled)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
