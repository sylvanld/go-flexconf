package flexvault

import "errors"

// Sentinel errors, usable with errors.Is. Drivers wrap these with backend
// detail via fmt.Errorf("...: %w", ErrX) but never embed secret values or
// credentials.
var (
	// ErrLocked reports that the vault is configured but not unlocked.
	ErrLocked = errors.New("flexvault: vault is locked")

	// ErrNotFound reports that the secret address does not exist in the vault.
	ErrNotFound = errors.New("flexvault: secret not found")

	// ErrReadOnly reports a write to a vault that is not writable.
	ErrReadOnly = errors.New("flexvault: vault is read-only")

	// ErrUnsupported reports an operation the driver does not support
	// (e.g. Create on a driver without Initializer).
	ErrUnsupported = errors.New("flexvault: operation not supported by driver")

	// ErrAuth reports failed authentication/unlock (bad master password,
	// invalid token) — distinct from other unlock failures such as a missing
	// or corrupt backend.
	ErrAuth = errors.New("flexvault: unlock failed")

	// ErrNotConfigured reports a lifecycle-order violation: Unlock/Get/Set/List
	// called before a successful Configure.
	ErrNotConfigured = errors.New("flexvault: driver not configured")
)
