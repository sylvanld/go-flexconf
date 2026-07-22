package agent

import (
	"fmt"
	"net"
	"time"
)

// Client is a connection to a running agent.
type Client struct {
	conn net.Conn
}

// Dial connects to the agent serving vaultID. It fails fast when no agent is
// listening.
func Dial(vaultID string) (*Client, error) {
	sock, err := socketPath(vaultID)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return nil, fmt.Errorf("agent: no agent for %s: %w", vaultID, err)
	}
	return &Client{conn: conn}, nil
}

// Running reports whether an agent is currently serving vaultID.
func Running(vaultID string) bool {
	c, err := Dial(vaultID)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) roundTrip(req *request) (*response, error) {
	if err := writeFrame(c.conn, req); err != nil {
		return nil, fmt.Errorf("agent: sending request: %w", err)
	}
	var resp response
	if err := readFrame(c.conn, &resp); err != nil {
		return nil, fmt.Errorf("agent: reading response: %w", err)
	}
	if !resp.OK {
		return nil, codeError(resp.Code, resp.Err)
	}
	return &resp, nil
}

// Unlock forwards already-collected credential answers to the agent.
func (c *Client) Unlock(answers map[string]string) error {
	_, err := c.roundTrip(&request{Op: "unlock", Answers: answers})
	return err
}

// Get fetches the secret at addr ("namespace/key").
func (c *Client) Get(addr string) (string, error) {
	resp, err := c.roundTrip(&request{Op: "get", Addr: addr})
	if err != nil {
		return "", err
	}
	return resp.Value, nil
}

// Set stores value at addr.
func (c *Client) Set(addr, value string) error {
	_, err := c.roundTrip(&request{Op: "set", Addr: addr, Value: value})
	return err
}

// List enumerates namespaces (ns == "") or keys within one.
func (c *Client) List(ns string) ([]string, error) {
	resp, err := c.roundTrip(&request{Op: "list", Namespace: ns})
	if err != nil {
		return nil, err
	}
	return resp.List, nil
}

// Status describes a running agent.
type Status struct {
	Unlocked bool
	VaultID  string
	IdleLeft time.Duration
	Writable bool
}

// Status queries the agent without resetting its idle timer.
func (c *Client) Status() (*Status, error) {
	resp, err := c.roundTrip(&request{Op: "status"})
	if err != nil {
		return nil, err
	}
	return &Status{
		Unlocked: resp.Unlocked,
		VaultID:  resp.VaultID,
		IdleLeft: time.Duration(resp.IdleLeft) * time.Millisecond,
		Writable: resp.Writable,
	}, nil
}

// Lock asks the agent to lock and exit gracefully.
func (c *Client) Lock() error {
	_, err := c.roundTrip(&request{Op: "lock"})
	return err
}
