package flexprompt

import "errors"

// Sentinel errors, usable with errors.Is.
var (
	// ErrPromptCancelled reports that the user aborted a prompt. It is
	// terminal: callers (e.g. the flexvault Manager) stop retrying on it.
	ErrPromptCancelled = errors.New("flexprompt: prompt cancelled")

	// ErrPromptUnavailable reports that a required request could not be
	// resolved by a non-interactive prompter.
	ErrPromptUnavailable = errors.New("flexprompt: no value for prompt")

	// ErrNoPrompter is returned by the default prompter installed when no
	// SetPrompter call was made; call SetPrompter to resolve it.
	ErrNoPrompter = errors.New("flexprompt: no prompter configured")
)
