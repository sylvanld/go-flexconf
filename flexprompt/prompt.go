// Package flexprompt provides credential/input collection for flexconf.
//
// A Prompter obtains input values (typically credentials) from wherever the
// program is running — an interactive terminal, a GUI dialog, or a
// non-interactive source (env vars, a preset map) in CI and tests. Components
// that need input declare PromptRequests; the installed Prompter answers them
// all in one interaction.
//
// flexprompt is the module's leaf package: it imports only the standard
// library (plus golang.org/x/term for masked terminal input).
package flexprompt

import (
	"context"
	"fmt"
)

// Prompter obtains input values from wherever flexconf is running. Interactive
// implementations ask a human; non-interactive ones resolve each req.ID from a
// preconfigured source (env, map, config file). Callers depend only on this
// interface.
type Prompter interface {
	// Dispatch asks for all reqs as a SINGLE interaction and returns the answers
	// keyed by PromptRequest.ID. Optional requests with no value are omitted from
	// the result; a required request with no value yields ErrPromptUnavailable.
	//
	// On any non-nil error the returned map is nil and callers MUST ignore it;
	// "omit a key but succeed" is reserved for the optional-with-no-value case.
	// Every req.ID MUST be non-empty and unique within one call; a duplicate or
	// empty ID is a programming error and Dispatch returns an error rather than
	// silently dropping a request. A cancelled or expired ctx yields an error
	// wrapping ctx.Err().
	Dispatch(ctx context.Context, reqs []PromptRequest) (map[string]string, error)
}

// PromptRequest describes one value a driver needs.
type PromptRequest struct {
	// ID is a stable machine key, e.g. "password", "keyfile-passphrase". It is
	// the join key: the driver declares it here and reads answers[ID] in Unlock.
	// Non-interactive prompters use it as the lookup key (env var, map key).
	ID string

	// Label is human-facing text for interactive prompters,
	// e.g. "KeePass master password for secrets.kdbx".
	Label string

	// Secret marks input that MUST be masked (not echoed) and MUST NOT be
	// logged. All credential requests set this true.
	Secret bool

	// Optional allows the value to be absent (key omitted from the answers map).
	Optional bool

	// Default is a suggested value for interactive prompters. MUST be empty
	// when Secret is true; built-in prompters defensively IGNORE Default when
	// Secret is true rather than panicking (a documented caller contract).
	Default string

	// Confirm asks interactive prompters to collect the value twice and require
	// the entries to match (double-entry). Used when a NEW secret is being set,
	// e.g. choosing a master password during vault creation. Non-interactive
	// prompters ignore it.
	Confirm bool
}

// PrompterFunc adapts a function to the Prompter interface.
type PrompterFunc func(context.Context, []PromptRequest) (map[string]string, error)

// Dispatch implements Prompter.
func (f PrompterFunc) Dispatch(ctx context.Context, reqs []PromptRequest) (map[string]string, error) {
	return f(ctx, reqs)
}

// validateRequests enforces the shared Dispatch contract: every ID non-empty
// and unique within one call. Built-in prompters call it first.
func validateRequests(reqs []PromptRequest) error {
	seen := make(map[string]struct{}, len(reqs))
	for _, r := range reqs {
		if r.ID == "" {
			return fmt.Errorf("flexprompt: prompt request with empty ID")
		}
		if _, dup := seen[r.ID]; dup {
			return fmt.Errorf("flexprompt: duplicate prompt request ID %q", r.ID)
		}
		seen[r.ID] = struct{}{}
	}
	return nil
}
