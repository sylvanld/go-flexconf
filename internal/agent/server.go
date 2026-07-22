package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sylvanld/go-flexconf/flexvault"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// server holds one unlocked Manager and serves requests over its socket.
type server struct {
	vaultID  string
	manager  *flexvault.Manager
	replay   *replayPrompter
	idle     time.Duration
	listener net.Listener

	reqMu    sync.Mutex // serializes request handling (single-owner model)
	mu       sync.Mutex // guards the idle timer state
	timer    *time.Timer
	deadline time.Time
	done     chan struct{}
	once     sync.Once
}

// idleTimeout reads the configured idle duration (FLEXCONF_IDLE_TIMEOUT, a Go
// duration string; zero/unset → 2m; negative → never).
func idleTimeout() time.Duration {
	if s := os.Getenv(envIdle); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d != 0 {
			return d
		}
	}
	return defaultIdleTimeout
}

// Serve runs the agent loop for vaultID over a Manager built around the
// vault's real driver (config re-read from the registry). It returns when the
// agent locks (idle timeout, lock request, or signal). Exposed for tests;
// production entry is RunAgentIfRequested.
func Serve(vaultID, vaultName string) error {
	reg, err := vaultreg.Load()
	if err != nil {
		return err
	}
	_, conf, err := reg.Resolve(vaultName)
	if err != nil {
		return err
	}
	drv, err := conf.BuildDriver()
	if err != nil {
		return err
	}
	// The agent never prompts: Unlock answers arrive over the socket and are
	// replayed to the Manager through a swappable prompter.
	replay := &replayPrompter{}
	mgr := flexvault.NewManager(drv, flexvault.WithPrompter(replay), flexvault.WithUnlockRetries(1))
	if err := mgr.Configure(flexvault.MapDecoder(conf.Config)); err != nil {
		return err
	}
	return serveManager(vaultID, mgr, replay)
}

// serveManager binds the socket and serves requests until locked.
func serveManager(vaultID string, mgr *flexvault.Manager, replay *replayPrompter) error {
	sock, err := socketPath(vaultID)
	if err != nil {
		return err
	}
	// Clean a stale socket only if nothing answers.
	if _, err := os.Stat(sock); err == nil {
		if c, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
			c.Close()
			return fmt.Errorf("agent: another agent is already bound to %s", sock)
		}
		os.Remove(sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("agent: binding %s: %w", sock, err)
	}
	os.Chmod(sock, 0o600)
	if pp, err := pidPath(vaultID); err == nil {
		os.WriteFile(pp, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}

	s := &server{
		vaultID:  vaultID,
		manager:  mgr,
		replay:   replay,
		idle:     idleTimeout(),
		listener: ln,
		done:     make(chan struct{}),
	}
	defer s.cleanup()

	// Lock and exit on termination signals.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sig:
			s.shutdown()
		case <-s.done:
		}
	}()

	s.resetIdle()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil // graceful shutdown
			default:
				return err
			}
		}
		// Connections are accepted concurrently, but requests are processed
		// one at a time (reqMu in handle) — the single-owner write model.
		go s.handleConn(conn)
	}
}

func (s *server) cleanup() {
	s.manager.Lock() // clears key material even on abnormal exit paths
	if sock, err := socketPath(s.vaultID); err == nil {
		os.Remove(sock)
	}
	if pp, err := pidPath(s.vaultID); err == nil {
		os.Remove(pp)
	}
}

func (s *server) shutdown() {
	s.once.Do(func() {
		close(s.done)
		s.listener.Close()
	})
}

func (s *server) resetIdle() {
	if s.idle < 0 {
		return // negative: never idle-locks (documented escape hatch)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadline = time.Now().Add(s.idle)
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.idle, s.shutdown)
}

// peerUIDMatches verifies the connecting peer's UID equals ours (SO_PEERCRED),
// in addition to filesystem permissions.
func peerUIDMatches(conn net.Conn) bool {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	matched := false
	raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		matched = err == nil && int(cred.Uid) == os.Getuid()
	})
	return matched
}

func (s *server) handleConn(conn net.Conn) {
	defer conn.Close()
	if !peerUIDMatches(conn) {
		return // reject foreign-UID peers outright
	}
	for {
		var req request
		if err := readFrame(conn, &req); err != nil {
			return // client closed (or garbage): drop the connection
		}
		resp := s.handle(&req)
		if err := writeFrame(conn, resp); err != nil {
			return
		}
		if req.Op == "lock" && resp.OK {
			s.shutdown()
			return
		}
	}
}

func (s *server) handle(req *request) *response {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if req.Op != "status" {
		s.resetIdle()
	}
	ctx := context.Background()
	switch req.Op {
	case "unlock":
		// The agent never prompts: it consumes already-collected answers via
		// a Manager whose prompter replays them.
		if s.manager.IsUnlocked() {
			return &response{OK: true}
		}
		err := s.unlockWith(ctx, req.Answers)
		clear(req.Answers)
		if err != nil {
			return errResponse(err)
		}
		return &response{OK: true}
	case "get":
		v, err := s.manager.Get(ctx, req.Addr)
		if err != nil {
			return errResponse(err)
		}
		return &response{OK: true, Value: v}
	case "set":
		if err := s.manager.Set(ctx, req.Addr, req.Value); err != nil {
			return errResponse(err)
		}
		return &response{OK: true}
	case "list":
		names, err := s.manager.List(ctx, req.Namespace)
		if err != nil {
			return errResponse(err)
		}
		if names == nil {
			names = []string{}
		}
		return &response{OK: true, List: names}
	case "status":
		s.mu.Lock()
		left := time.Until(s.deadline)
		s.mu.Unlock()
		return &response{
			OK:       true,
			Unlocked: s.manager.IsUnlocked(),
			VaultID:  s.vaultID,
			IdleLeft: left.Milliseconds(),
			Writable: s.manager.Capabilities().Writable,
		}
	case "lock":
		if err := s.manager.Lock(); err != nil {
			return errResponse(err)
		}
		return &response{OK: true}
	default:
		return &response{Err: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// unlockWith drives Manager.Unlock, replaying the forwarded answers through
// the Manager's prompter.
func (s *server) unlockWith(ctx context.Context, answers map[string]string) error {
	s.replay.set(answers)
	defer s.replay.clear()
	return s.manager.Unlock(ctx)
}

func errResponse(err error) *response {
	return &response{Err: err.Error(), Code: errorCode(err)}
}
