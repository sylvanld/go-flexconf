package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/sylvanld/go-flexconf/flexprompt"
)

// replayPrompter feeds already-collected answers to a Manager's Dispatch —
// the agent never prompts a user itself.
type replayPrompter struct {
	mu      sync.Mutex
	answers map[string]string
}

func (p *replayPrompter) set(answers map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers = answers
}

func (p *replayPrompter) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.answers)
	p.answers = nil
}

func (p *replayPrompter) Dispatch(_ context.Context, reqs []flexprompt.PromptRequest) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(reqs))
	for _, r := range reqs {
		v, ok := p.answers[r.ID]
		if !ok {
			if r.Optional {
				continue
			}
			return nil, fmt.Errorf("agent: no forwarded answer for %q: %w", r.ID, flexprompt.ErrPromptUnavailable)
		}
		out[r.ID] = v
	}
	return out, nil
}
