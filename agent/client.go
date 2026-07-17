package agent

import (
	"bufio"
	"encoding/json"
	"net"
	"time"

	"forgejo.ovhcloud.tools/sylvan/flexconf/secrets"
)

// Client is a secrets.Driver that forwards operations to a running agent over a
// Unix socket. It supports reads and writes; the agent performs the actual
// KeePass access with its held credentials.
type Client struct {
	SocketPath string

	// DialTimeout bounds how long a connect attempt waits. Zero uses a default.
	DialTimeout time.Duration
}

// Ensure Client satisfies the Driver contract.
var _ secrets.Driver = (*Client)(nil)

// NewClient returns a Client for the socket at path.
func NewClient(path string) *Client {
	return &Client{SocketPath: path}
}

func (c *Client) dialTimeout() time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	return 2 * time.Second
}

// IsRunning reports whether an agent is reachable on the socket.
func (c *Client) IsRunning() bool {
	conn, err := net.DialTimeout("unix", c.SocketPath, c.dialTimeout())
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *Client) do(req Request) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.SocketPath, c.dialTimeout())
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Unlock verifies the agent is reachable. The agent is already unlocked, so no
// password is required here.
func (c *Client) Unlock() error {
	resp, err := c.do(Request{Op: OpStatus})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errFromKind(resp.ErrKind, resp.Err)
	}
	return nil
}

// Get returns the secret stored under key.
func (c *Client) Get(key string) (*secrets.Secret, error) {
	resp, err := c.do(Request{Op: OpGet, Key: key})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errFromKind(resp.ErrKind, resp.Err)
	}
	return resp.Secret, nil
}

// List returns every secret held by the agent.
func (c *Client) List() ([]secrets.Secret, error) {
	resp, err := c.do(Request{Op: OpList})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errFromKind(resp.ErrKind, resp.Err)
	}
	return resp.Secrets, nil
}

// Set stores a secret via the agent.
func (c *Client) Set(s secrets.Secret) error {
	resp, err := c.do(Request{Op: OpSet, Secret: &s})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errFromKind(resp.ErrKind, resp.Err)
	}
	return nil
}

// Delete removes the secret stored under key via the agent.
func (c *Client) Delete(key string) error {
	resp, err := c.do(Request{Op: OpDelete, Key: key})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errFromKind(resp.ErrKind, resp.Err)
	}
	return nil
}

// Lock asks the agent to shut down and drop its cached secrets.
func (c *Client) Lock() error {
	resp, err := c.do(Request{Op: OpLock})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errFromKind(resp.ErrKind, resp.Err)
	}
	return nil
}
