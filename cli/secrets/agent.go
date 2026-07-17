package secrets

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sylvanld/flexconf/agent"
	"github.com/sylvanld/flexconf/secrets"
)

// agentGroup builds the "agent" command group and its sub-commands.
func (c *cli) agentGroup() *cobra.Command {
	g := &cobra.Command{
		Use:          "agent",
		Short:        "Manage the unlock agent",
		Long:         "Manage the short-lived agent that keeps the store unlocked so a single\npassword prompt covers repeated commands.",
		SilenceUsage: true,
	}
	c.runCmd = c.agentRunCmd()
	g.AddCommand(
		c.agentUnlockCmd(),
		c.agentLockCmd(),
		c.agentStatusCmd(),
		c.runCmd,
	)
	return g
}

// agentUnlockCmd starts the agent explicitly (what other commands do implicitly).
func (c *cli) agentUnlockCmd() *cobra.Command {
	var idle, max time.Duration
	cmd := &cobra.Command{
		Use:          "unlock",
		Short:        "Start the agent (prompts once, caches for a few minutes)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent.NewClient(c.socket()).IsRunning() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent already running")
				return nil
			}
			return c.startAgentDetached(idle, max)
		},
	}
	cmd.Flags().DurationVar(&idle, "idle", c.idle, "idle timeout before locking")
	cmd.Flags().DurationVar(&max, "max", c.max, "absolute lifetime before locking")
	return cmd
}

// agentLockCmd stops a running agent.
func (c *cli) agentLockCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "lock",
		Short:        "Stop the running agent and drop its cached secrets",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := agent.NewClient(c.socket())
			if !client.IsRunning() {
				fmt.Fprintln(cmd.ErrOrStderr(), "no agent running")
				return nil
			}
			if err := client.Lock(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "agent locked")
			return nil
		},
	}
}

// agentStatusCmd reports whether an agent is running.
func (c *cli) agentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show whether an agent is running",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock := c.socket()
			if agent.NewClient(sock).IsRunning() {
				fmt.Fprintf(cmd.OutOrStdout(), "agent running (socket %s)\n", sock)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "no agent running (socket %s)\n", sock)
			}
			return nil
		},
	}
}

// agentRunCmd runs the agent in the foreground. It is normally invoked by the
// detached process that "unlock" spawns; the --kdbx flag lets that parent hand
// the child the exact store path regardless of how the app resolves settings.
func (c *cli) agentRunCmd() *cobra.Command {
	var idle, max time.Duration
	var kdbx string
	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Run the agent in the foreground",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := c.kdbx
			if kdbx != "" {
				path = kdbx
			}
			return c.runAgent(idle, max, path)
		},
	}
	cmd.Flags().DurationVar(&idle, "idle", c.idle, "idle timeout before locking")
	cmd.Flags().DurationVar(&max, "max", c.max, "absolute lifetime before locking")
	cmd.Flags().StringVar(&kdbx, "kdbx", "", "explicit KeePass file path (used by the detached agent)")
	_ = cmd.Flags().MarkHidden("kdbx")
	return cmd
}

// runAgent unlocks the KeePass file and serves it over the socket until the
// idle/max timeout, a lock request, or a signal.
func (c *cli) runAgent(idle, max time.Duration, kdbx string) error {
	// Read-write driver: the agent serves writes too, so it keeps the master
	// credentials for its lifetime.
	driver := secrets.NewKeepassDriver(kdbx)
	if pw, ok := c.passwordFromPipe(); ok {
		driver.PromptPassword = func() (string, error) { return pw, nil }
	}
	if err := driver.Unlock(); err != nil {
		return err
	}

	// Best-effort process hardening (no core dumps, keep pages out of swap).
	// Done AFTER unlock so the memory-hard KDF's large transient allocation is
	// not forced under mlock (which could exceed RLIMIT_MEMLOCK and fail).
	if err := agent.Harden(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: hardening: %v\n", err)
	}

	sock := c.socket()
	srv := agent.NewServer(driver, sock)
	srv.IdleTTL = idle
	srv.MaxLifetime = max

	// Bind before announcing, so a refused (already-running) start reports an
	// error instead of a misleading "listening" line.
	if err := srv.Listen(); err != nil {
		return err
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		srv.Stop()
	}()

	fmt.Fprintf(os.Stderr, "agent listening on %s (idle %s, max %s)\n", sock, idle, max)
	return srv.Serve()
}

// startAgentDetached prompts for the password in the current shell, then spawns
// a detached agent running "<this command path> agent run", handing the password
// over an inherited pipe (never argv or the environment), and waits for its
// socket to appear.
func (c *cli) startAgentDetached(idle, max time.Duration) error {
	sock := c.socket()

	fmt.Fprintf(os.Stderr, "Password for %s: ", c.kdbx)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}

	pr, pw2, err := os.Pipe()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}

	// Capture the detached agent's output to a log file so a startup failure is
	// diagnosable (otherwise it would vanish).
	logPath := c.cfg.File("agent.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	cmd := exec.Command(self, c.childRunArgs(idle, max)...)
	cmd.Env = append(os.Environ(), c.envFD+"=3")
	cmd.ExtraFiles = []*os.File{pr} // inherited as fd 3 in the child
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachAttr()

	if err := cmd.Start(); err != nil {
		pr.Close()
		pw2.Close()
		logf.Close()
		return err
	}
	pr.Close()   // the child holds its own copy
	logf.Close() // the child holds its own dup

	if _, err := pw2.Write([]byte(pw)); err != nil {
		pw2.Close()
		return err
	}
	pw2.Close()

	// Reap the child in the background so we can detect an early exit (e.g. wrong
	// password) without blocking the success path, where it keeps running.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	client := agent.NewClient(sock)
	for i := 0; i < 200; i++ {
		if client.IsRunning() {
			fmt.Fprintf(os.Stderr, "agent unlocked (socket %s)\n", sock)
			return nil
		}
		select {
		case werr := <-exited:
			return agentStartError(logPath, fmt.Sprintf("agent process exited (%v)", werr))
		case <-time.After(50 * time.Millisecond):
		}
	}
	return agentStartError(logPath, "timed out waiting for agent socket")
}

// childRunArgs reconstructs the argv (after the program name) that re-invokes
// this binary at the "agent run" command, wherever it sits in the host app's
// command tree.
func (c *cli) childRunArgs(idle, max time.Duration) []string {
	// CommandPath is e.g. "myapp secrets agent run"; drop the program name and
	// keep the sub-command path so the child routes to the same command.
	parts := strings.Fields(c.runCmd.CommandPath())
	args := append([]string{}, parts[1:]...)
	return append(args,
		"--idle", idle.String(),
		"--max", max.String(),
		"--kdbx", c.kdbx,
	)
}

// agentStartError builds an error including the tail of the agent log.
func agentStartError(logPath, reason string) error {
	data, _ := os.ReadFile(logPath)
	out := strings.TrimSpace(string(data))
	if out == "" {
		return fmt.Errorf("agent did not start: %s", reason)
	}
	return fmt.Errorf("agent did not start: %s\n%s", reason, out)
}

// passwordFromPipe reads a password from the inherited fd named by the app's
// password-fd environment variable, if present. Returns false when the process
// was not launched by the detached-unlock path.
func (c *cli) passwordFromPipe() (string, bool) {
	v := os.Getenv(c.envFD)
	if v == "" {
		return "", false
	}
	fd, err := strconv.Atoi(v)
	if err != nil {
		return "", false
	}
	f := os.NewFile(uintptr(fd), "password")
	if f == nil {
		return "", false
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(data), "\r\n"), true
}
