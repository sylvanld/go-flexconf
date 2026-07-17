// Package agent provides a small background process that holds an unlocked
// secrets.Driver in memory for a bounded time and serves its operations over a
// per-user Unix socket, plus a client that speaks the same protocol as a
// secrets.Driver. This lets a CLI unlock once and avoid re-prompting for the
// master password on every invocation.
//
// The agent serves writes as well as reads, so a single unlock covers every
// command. The cost is that the served driver must retain the master
// credentials in memory (to re-encrypt on write) for the lifetime of the agent.
package agent

import (
	"errors"

	"forgejo.ovhcloud.tools/sylvan/flexconf/secrets"
)

// Protocol operations.
const (
	OpGet    = "get"
	OpSet    = "set"
	OpList   = "list"
	OpDelete = "delete"
	OpStatus = "status"
	OpLock   = "lock"
)

// Error kinds carried in a Response so the client can reconstruct sentinel
// errors on its side (keeping errors.Is working across the socket).
const (
	errKindNotFound = "not_found"
	errKindEmptyKey = "empty_key"
	errKindReadOnly = "read_only"
	errKindInternal = "internal"
)

// Request is a single agent command.
type Request struct {
	Op     string          `json:"op"`
	Key    string          `json:"key,omitempty"`
	Secret *secrets.Secret `json:"secret,omitempty"` // for OpSet
}

// Response is the agent's reply to a Request.
type Response struct {
	OK      bool             `json:"ok"`
	Secret  *secrets.Secret  `json:"secret,omitempty"`
	Secrets []secrets.Secret `json:"secrets,omitempty"`
	ErrKind string           `json:"errKind,omitempty"`
	Err     string           `json:"err,omitempty"`
}

// errKindOf maps a driver error to a stable wire code.
func errKindOf(err error) string {
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return errKindNotFound
	case errors.Is(err, secrets.ErrEmptyKey):
		return errKindEmptyKey
	case errors.Is(err, secrets.ErrReadOnly):
		return errKindReadOnly
	default:
		return errKindInternal
	}
}

// errFromKind reconstructs an error from a wire code, preferring the matching
// secrets sentinel so errors.Is keeps working on the client side.
func errFromKind(kind, msg string) error {
	switch kind {
	case errKindNotFound:
		return secrets.ErrNotFound
	case errKindEmptyKey:
		return secrets.ErrEmptyKey
	case errKindReadOnly:
		return secrets.ErrReadOnly
	default:
		if msg == "" {
			msg = "agent: internal error"
		}
		return errors.New(msg)
	}
}
