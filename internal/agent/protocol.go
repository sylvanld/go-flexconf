package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sylvanld/go-flexconf/flexvault"
)

// request is one client → agent message. The wire encoding (length-prefixed
// JSON) is an implementation detail of this internal package.
type request struct {
	Op        string            `json:"op"` // unlock | get | set | list | status | lock
	Addr      string            `json:"addr,omitempty"`
	Value     string            `json:"value,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
}

// response is one agent → client message. Err/Code map back to the flexvault
// sentinels so callers can errors.Is them.
type response struct {
	OK       bool     `json:"ok"`
	Value    string   `json:"value,omitempty"`
	List     []string `json:"list,omitempty"`
	Err      string   `json:"err,omitempty"`
	Code     string   `json:"code,omitempty"`
	Unlocked bool     `json:"unlocked,omitempty"`
	VaultID  string   `json:"vault_id,omitempty"`
	IdleLeft int64    `json:"idle_left_ms,omitempty"`
	Writable bool     `json:"writable,omitempty"`
}

// errorCode maps a server-side error to a stable wire code.
func errorCode(err error) string {
	switch {
	case errors.Is(err, flexvault.ErrLocked):
		return "locked"
	case errors.Is(err, flexvault.ErrNotFound):
		return "notfound"
	case errors.Is(err, flexvault.ErrReadOnly):
		return "readonly"
	case errors.Is(err, flexvault.ErrAuth):
		return "auth"
	case errors.Is(err, flexvault.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, flexvault.ErrNotConfigured):
		return "notconfigured"
	default:
		return ""
	}
}

// codeError maps a wire code back to the sentinel, wrapping the remote
// message.
func codeError(code, msg string) error {
	var sentinel error
	switch code {
	case "locked":
		sentinel = flexvault.ErrLocked
	case "notfound":
		sentinel = flexvault.ErrNotFound
	case "readonly":
		sentinel = flexvault.ErrReadOnly
	case "auth":
		sentinel = flexvault.ErrAuth
	case "unsupported":
		sentinel = flexvault.ErrUnsupported
	case "notconfigured":
		sentinel = flexvault.ErrNotConfigured
	default:
		return fmt.Errorf("agent: %s", msg)
	}
	return fmt.Errorf("agent: %s: %w", msg, sentinel)
}

const maxFrame = 16 << 20 // sanity cap on frame size

// writeFrame writes one length-prefixed JSON message.
func writeFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// readFrame reads one length-prefixed JSON message into v.
func readFrame(r io.Reader, v any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > maxFrame {
		return fmt.Errorf("agent: frame too large (%d bytes)", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// defaultIdleTimeout is how long the agent stays unlocked with no request.
const defaultIdleTimeout = 2 * time.Minute
