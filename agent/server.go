package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sylvanld/flexconf/secrets"
)

// Default expiry values.
const (
	defaultIdleTTL     = 5 * time.Minute
	defaultMaxLifetime = 30 * time.Minute
)

// Server serves access to an already-unlocked secrets.Driver over a Unix socket
// for a bounded time, then locks (drops the driver, removes the socket, and
// returns).
type Server struct {
	// Driver is an already-unlocked driver. It is used for both reads and writes,
	// so it retains the master credentials for the agent's lifetime.
	Driver secrets.Driver
	// SocketPath is where the Unix socket is created.
	SocketPath string
	// IdleTTL locks the agent after this much inactivity (default 5m).
	IdleTTL time.Duration
	// MaxLifetime caps the total lifetime regardless of activity (default 30m).
	MaxLifetime time.Duration

	mu           sync.Mutex
	listener     net.Listener
	activity     chan struct{}
	done         chan struct{}
	shutdownOnce sync.Once
	stopOnce     sync.Once
}

// NewServer returns a Server serving driver on the socket at socketPath.
func NewServer(driver secrets.Driver, socketPath string) *Server {
	return &Server{
		Driver:     driver,
		SocketPath: socketPath,
		activity:   make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

func (s *Server) idleTTL() time.Duration {
	if s.IdleTTL > 0 {
		return s.IdleTTL
	}
	return defaultIdleTTL
}

func (s *Server) maxLifetime() time.Duration {
	if s.MaxLifetime > 0 {
		return s.MaxLifetime
	}
	return defaultMaxLifetime
}

// Serve runs the agent until the idle TTL or max lifetime elapses, a lock
// request arrives, or Stop is called. It always removes the socket and drops
// the driver reference before returning.
func (s *Server) Serve() error {
	if s.Driver == nil {
		return secrets.ErrNoDriver
	}
	if s.activity == nil {
		s.activity = make(chan struct{}, 1)
	}
	if s.done == nil {
		s.done = make(chan struct{})
	}
	if s.listener == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}

	go s.acceptLoop()

	idle := time.NewTimer(s.idleTTL())
	max := time.NewTimer(s.maxLifetime())
	defer idle.Stop()
	defer max.Stop()

	for {
		select {
		case <-s.activity:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(s.idleTTL())
		case <-idle.C:
			return s.shutdown()
		case <-max.C:
			return s.shutdown()
		case <-s.done:
			return s.shutdown()
		}
	}
}

// Stop triggers an orderly shutdown of a running Serve (e.g. on a signal).
func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.done) })
}

// Listen binds the Unix socket, creating its 0700 directory and setting the
// socket to 0600. It refuses to start if a live agent already owns the socket,
// and cleans up a stale socket otherwise. Serve calls Listen automatically;
// call it explicitly first when you want the bind to succeed (or fail) before
// entering the blocking serve loop. It is a no-op if already listening.
func (s *Server) Listen() error {
	if s.listener != nil {
		return nil
	}
	dir := filepath.Dir(s.SocketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// If a socket file exists, either another agent is live (refuse) or it is
	// stale (clean up).
	if _, err := os.Stat(s.SocketPath); err == nil {
		if (&Client{SocketPath: s.SocketPath}).IsRunning() {
			return fmt.Errorf("agent: already running at %s", s.SocketPath)
		}
		_ = os.Remove(s.SocketPath)
	}

	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.listener = ln
	return nil
}

func (s *Server) shutdown() error {
	s.shutdownOnce.Do(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
		_ = os.Remove(s.SocketPath)
		s.mu.Lock()
		s.Driver = nil // drop the reference so the decrypted store can be GC'd
		s.mu.Unlock()
	})
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed → shutting down
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	// Only accept connections from the same user.
	if err := checkPeer(conn); err != nil {
		writeResponse(conn, Response{OK: false, ErrKind: errKindInternal, Err: "access denied"})
		return
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, Response{OK: false, ErrKind: errKindInternal, Err: "bad request"})
		return
	}

	// Reset the idle timer on any request (non-blocking).
	select {
	case s.activity <- struct{}{}:
	default:
	}

	writeResponse(conn, s.dispatch(req))

	if req.Op == OpLock {
		s.Stop()
	}
}

func (s *Server) dispatch(req Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Driver == nil {
		return Response{OK: false, ErrKind: errKindInternal, Err: "agent locked"}
	}

	switch req.Op {
	case OpStatus, OpLock:
		return Response{OK: true}
	case OpGet:
		secret, err := s.Driver.Get(req.Key)
		if err != nil {
			return Response{OK: false, ErrKind: errKindOf(err), Err: err.Error()}
		}
		return Response{OK: true, Secret: secret}
	case OpList:
		list, err := s.Driver.List()
		if err != nil {
			return Response{OK: false, ErrKind: errKindOf(err), Err: err.Error()}
		}
		return Response{OK: true, Secrets: list}
	case OpSet:
		if req.Secret == nil {
			return Response{OK: false, ErrKind: errKindInternal, Err: "set requires a secret"}
		}
		if err := s.Driver.Set(*req.Secret); err != nil {
			return Response{OK: false, ErrKind: errKindOf(err), Err: err.Error()}
		}
		return Response{OK: true}
	case OpDelete:
		if err := s.Driver.Delete(req.Key); err != nil {
			return Response{OK: false, ErrKind: errKindOf(err), Err: err.Error()}
		}
		return Response{OK: true}
	default:
		return Response{OK: false, ErrKind: errKindInternal, Err: "unsupported operation: " + req.Op}
	}
}

func writeResponse(conn net.Conn, resp Response) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(payload, '\n'))
}
