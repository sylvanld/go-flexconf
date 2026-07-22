package flexprompt

import (
	"context"
	"fmt"
	"sync/atomic"
)

// current holds the process-wide Prompter. It stores a prompterBox so that
// storing implementations of different concrete types never panics
// (atomic.Value requires a consistent concrete type).
var current atomic.Value // of prompterBox

type prompterBox struct{ p Prompter }

// errorPrompter is the default when no prompter has been installed: every
// required request fails with ErrNoPrompter; optional requests are omitted.
type errorPrompter struct{}

func (errorPrompter) Dispatch(_ context.Context, reqs []PromptRequest) (map[string]string, error) {
	if err := validateRequests(reqs); err != nil {
		return nil, err
	}
	for _, r := range reqs {
		if !r.Optional {
			return nil, fmt.Errorf("flexprompt: required prompt %q: %w", r.ID, ErrNoPrompter)
		}
	}
	return map[string]string{}, nil
}

// SetPrompter installs the process-wide Prompter. Typically called once at
// startup, e.g. flexprompt.SetPrompter(flexprompt.NewCLIPrompter()).
// Safe for concurrent use. SetPrompter(nil) resets to the default error
// prompter, which is convenient for restoring state between tests.
func SetPrompter(p Prompter) {
	current.Store(prompterBox{p: p})
}

// GetPrompter returns the process-wide Prompter. It never returns nil: if none
// has been set it returns a default prompter whose Dispatch fails every
// required request with ErrNoPrompter. Safe for concurrent use.
func GetPrompter() Prompter {
	if box, ok := current.Load().(prompterBox); ok && box.p != nil {
		return box.p
	}
	return errorPrompter{}
}
