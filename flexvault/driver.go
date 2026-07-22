// Package flexvault provides pluggable secret backends for flexconf.
//
// A VaultDriver implements the mechanics of one backend (KeePass, HashiCorp
// Vault, …); a Manager wraps a single driver and drives its lifecycle:
// Configure → Credentials → Dispatch (via flexprompt) → Unlock → Get/Set/List
// → Lock. Drivers register under a name so the backend is selected at runtime.
package flexvault

import (
	"context"

	"github.com/sylvanld/go-flexconf/flexprompt"
)

// VaultDriver is implemented by each secret backend. A driver instance manages
// access to exactly one vault. The Manager serializes calls, so implementations
// need not be safe for concurrent use unless they document otherwise.
type VaultDriver interface {
	// Name returns the stable driver identifier (e.g. "keepass", "vault"),
	// used for registration/selection.
	Name() string

	// Configure loads this driver's non-secret settings. decode unmarshals the
	// driver's configuration section into a value the driver owns (typically a
	// driver-defined struct). Configure MUST NOT contact the backend and MUST
	// NOT read secrets. Called once, before Credentials/Unlock.
	Configure(decode func(target any) error) error

	// Credentials declares the secret values this driver needs to unlock, based
	// on its configured state (e.g. password Optional when a keyfile is set).
	// Returns an empty slice if no secret input is needed. Each request's ID is
	// the key the driver will read from Unlock's answers map.
	Credentials() []flexprompt.PromptRequest

	// Unlock opens the vault using the configured settings and the answers
	// gathered for the requests from Credentials (keyed by PromptRequest.ID).
	// It MUST succeed before Get/Set/List work. On bad credentials it MUST
	// return ErrAuth (wrapped with detail) and leave the vault locked. Drivers
	// SHOULD discard plaintext answers once derived material is computed.
	Unlock(ctx context.Context, answers map[string]string) error

	// Capabilities reports what this configured vault supports (e.g. writes).
	// Before Configure it returns the zero value.
	Capabilities() Capabilities

	// Get retrieves the secret value at "namespace/key". ErrNotFound if
	// absent, ErrLocked if not unlocked. A secret is a single string value.
	Get(ctx context.Context, addr string) (string, error)

	// Set stores value at "namespace/key" (create or overwrite, auto-creating
	// the namespace). ErrReadOnly if the vault is not writable, ErrLocked if
	// not unlocked.
	Set(ctx context.Context, addr string, value string) error

	// List enumerates the vault: with namespace == "" it returns the
	// namespaces; with a namespace it returns the key names within it. An
	// unknown namespace yields an empty slice and nil error. ErrLocked if not
	// unlocked.
	List(ctx context.Context, namespace string) ([]string, error)

	// Lock releases access and clears sensitive in-memory material. After Lock,
	// Get/Set/List MUST return ErrLocked until Unlock succeeds again. Lock on an
	// already-locked driver is a no-op returning nil.
	Lock() error
}

// Initializer is implemented by drivers that can create a new, empty vault.
// A driver that implements it MUST report Capabilities().Creatable == true.
type Initializer interface {
	// InitCredentials declares the secret values needed to create the vault
	// (e.g. a new master password, typically with PromptRequest.Confirm set).
	// Distinct from Credentials(), which unlocks an EXISTING vault.
	InitCredentials() []flexprompt.PromptRequest

	// Init creates a new, empty vault at the configured location using answers
	// gathered for InitCredentials(). It MUST fail (not overwrite) if the vault
	// already exists. On success the vault is created but NOT left unlocked.
	Init(ctx context.Context, answers map[string]string) error
}

// Capabilities reports what a configured vault supports. It has room to grow.
type Capabilities struct {
	Writable  bool // Set is supported (e.g. KeePass opened read/write)
	Creatable bool // the driver can create a brand-new empty vault (Initializer)
}
