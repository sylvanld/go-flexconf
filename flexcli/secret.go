package flexcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/sylvanld/go-flexconf/flexvault"
	"github.com/sylvanld/go-flexconf/internal/agent"
	"github.com/sylvanld/go-flexconf/internal/vaultreg"
)

// cmdState carries per-invocation flag state.
type cmdState struct {
	app       *App
	vaultFlag string
	noUnlock  bool
	forceUnlk bool
}

// SecretCommand returns the `secret` Cobra command group to mount under an
// app's root command. The persistent --vault flag selects a registry vault,
// overriding the App/registry default.
func (a *App) SecretCommand() *cobra.Command {
	st := &cmdState{app: a}
	group := &cobra.Command{
		Use:           "secret",
		Aliases:       []string{"secrets"},
		Short:         "Manage secrets in the configured vaults",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	group.PersistentFlags().StringVar(&st.vaultFlag, "vault", "", "registry vault to target (default: the registry's default vault)")

	group.AddCommand(
		st.initCmd(),
		st.unlockCmd(),
		st.lockCmd(),
		st.getCmd(),
		st.setCmd(),
		st.listCmd(),
		st.statusCmd(),
		st.vaultsCmd(),
	)
	return group
}

// selectVault resolves the target vault: --vault flag > FLEXCONF_VAULT env >
// Options.DefaultVault > the registry's default.
func (st *cmdState) selectVault() (name string, conf vaultreg.VaultConf, vaultID string, err error) {
	reg, err := vaultreg.Load()
	if err != nil {
		return "", vaultreg.VaultConf{}, "", err
	}
	requested := st.vaultFlag
	if requested == "" {
		requested = os.Getenv("FLEXCONF_VAULT")
	}
	if requested == "" {
		requested = st.app.opts.DefaultVault
	}
	name, conf, err = reg.Resolve(requested)
	if err != nil {
		return "", vaultreg.VaultConf{}, "", err
	}
	vaultID = st.app.opts.VaultID
	if vaultID == "" {
		vaultID = vaultreg.VaultID(name, conf)
	}
	return name, conf, vaultID, nil
}

func (st *cmdState) initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a new, empty vault (never overwrites an existing one)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, conf, _, err := st.selectVault()
			if err != nil {
				return err
			}
			drv, err := flexvault.New(conf.Driver)
			if err != nil {
				return err
			}
			mgr := flexvault.NewManager(drv, flexvault.WithPrompter(st.app.prompter()))
			if err := mgr.Create(cmd.Context(), flexvault.MapDecoder(conf.Config)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created vault %q\n", name)
			return nil
		},
	}
}

func (st *cmdState) unlockCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault into a background agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _, vaultID, err := st.selectVault()
			if err != nil {
				return err
			}
			if agent.Running(vaultID) {
				if !force {
					fmt.Fprintln(cmd.OutOrStdout(), "already unlocked")
					return nil
				}
				if c, err := agent.Dial(vaultID); err == nil {
					c.Lock()
					c.Close()
				}
			}
			if err := st.unlockAgent(cmd.Context(), name, vaultID); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "unlocked; agent will lock after idle timeout")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-prompt and re-unlock even if an agent is running")
	return cmd
}

// unlockAgent runs the CLI unlock flow: collect credentials in the
// foreground, ensure an agent, forward the answers, wipe them.
func (st *cmdState) unlockAgent(ctx context.Context, vaultName, vaultID string) error {
	proxy, err := agent.NewProxy(vaultName)
	if err != nil {
		return err
	}
	agent.MarkEntryPointWired() // the CLI always wires RunAgentIfRequested
	mgr := flexvault.NewManager(proxy, flexvault.WithPrompter(st.app.prompter()))
	if err := mgr.Configure(flexvault.MapDecoder(nil)); err != nil {
		return err
	}
	if err := mgr.Unlock(ctx); err != nil {
		return err
	}
	return nil
}

func (st *cmdState) lockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Lock the vault and stop its agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, vaultID, err := st.selectVault()
			if err != nil {
				return err
			}
			client, err := agent.Dial(vaultID)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "no agent running")
				return nil
			}
			defer client.Close()
			if err := client.Lock(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "locked; agent stopped")
			return nil
		},
	}
}

// withAgent dials the agent, applying the §4.1 auto-unlock behaviour when
// none is running: prompt on a TTY (or --unlock), fail otherwise.
func (st *cmdState) withAgent(cmd *cobra.Command) (*agent.Client, error) {
	name, _, vaultID, err := st.selectVault()
	if err != nil {
		return nil, err
	}
	if client, err := agent.Dial(vaultID); err == nil {
		return client, nil
	}
	// No agent: decide whether to auto-unlock.
	allowed := st.forceUnlk
	if !allowed && !st.noUnlock && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Vault is locked. Unlock now? [y/N] ")
		var answer string
		fmt.Fscanln(cmd.InOrStdin(), &answer)
		allowed = strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	}
	if !allowed {
		return nil, fmt.Errorf("vault %q is locked — run `secret unlock` first: %w", name, flexvault.ErrLocked)
	}
	if err := st.unlockAgent(cmd.Context(), name, vaultID); err != nil {
		return nil, err
	}
	return agent.Dial(vaultID)
}

func (st *cmdState) addUnlockFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&st.forceUnlk, "unlock", false, "auto-unlock without asking when no agent is running")
	cmd.Flags().BoolVar(&st.noUnlock, "no-unlock", false, "never auto-unlock; fail when no agent is running")
}

func (st *cmdState) getCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "get <namespace/key>",
		Short: "Print a secret value to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, _, err := flexvault.ParseAddress(args[0]); err != nil {
				return err
			}
			client, err := st.withAgent(cmd)
			if err != nil {
				return err
			}
			defer client.Close()
			value, err := client.Get(args[0])
			if err != nil {
				return err
			}
			if raw {
				fmt.Fprint(cmd.OutOrStdout(), value)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), value)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit the exact stored value with nothing added (safe for multi-line secrets)")
	st.addUnlockFlags(cmd)
	return cmd
}

func (st *cmdState) setCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <namespace/key>",
		Short: "Store a secret value read from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, _, err := flexvault.ParseAddress(args[0]); err != nil {
				return err
			}
			// The value comes from stdin, never argv (no shell history leak).
			data, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("reading value from stdin: %w", err)
			}
			client, err := st.withAgent(cmd)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.Set(args[0], string(data)); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	st.addUnlockFlags(cmd)
	return cmd
}

func (st *cmdState) listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [namespace]",
		Short: "List namespaces, or the keys within one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ns := ""
			if len(args) == 1 {
				ns = args[0]
			}
			client, err := st.withAgent(cmd)
			if err != nil {
				return err
			}
			defer client.Close()
			names, err := client.List(ns)
			if err != nil {
				return err
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
	st.addUnlockFlags(cmd)
	return cmd
}

func (st *cmdState) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the agent's state without resetting its idle timer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, vaultID, err := st.selectVault()
			if err != nil {
				return err
			}
			client, err := agent.Dial(vaultID)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "locked (no agent running)")
				return nil
			}
			defer client.Close()
			status, err := client.Status()
			if err != nil {
				return err
			}
			state := "locked"
			if status.Unlocked {
				state = "unlocked"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  vault-id=%s  idle-remaining=%s  writable=%v\n",
				state, status.VaultID, status.IdleLeft.Round(1e9), status.Writable)
			return nil
		},
	}
}

func (st *cmdState) vaultsCmd() *cobra.Command {
	var format string
	var validate bool
	cmd := &cobra.Command{
		Use:   "vaults",
		Short: "Show the resolved vault registry and its file provenance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := vaultreg.Load()
			if err != nil {
				return err
			}
			if format == "yaml" {
				return dumpRegistryYAML(cmd.OutOrStdout(), reg)
			}
			notes := dumpRegistryHuman(cmd.OutOrStdout(), reg)
			if validate && len(notes) > 0 {
				return fmt.Errorf("registry validation failed:\n  %s", strings.Join(notes, "\n  "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "", `output format ("yaml" for a machine-readable dump)`)
	cmd.Flags().BoolVar(&validate, "validate", false, "treat registry problems as errors (non-zero exit) for CI use")
	return cmd
}

func dumpRegistryHuman(w io.Writer, reg *vaultreg.Registry) (notes []string) {
	fmt.Fprintln(w, "registry files (in order):")
	for i, f := range reg.Files {
		mark := "[missing]"
		if f.Exists {
			mark = "[ok]"
		}
		src := ""
		if f.FromEnv {
			src = fmt.Sprintf("   (%s #%d)", vaultreg.EnvVaults, i+1)
		}
		fmt.Fprintf(w, "  %d. %s   %s%s\n", i+1, f.Path, mark, src)
		if f.FromEnv && !f.Exists {
			notes = append(notes, fmt.Sprintf("file named in %s could not be read: %s", vaultreg.EnvVaults, f.Path))
		}
	}
	fmt.Fprintln(w)
	if reg.Default != "" {
		fmt.Fprintf(w, "default: %s   (set by %s)\n", reg.Default, reg.DefaultSource)
		if _, ok := reg.Vaults[reg.Default]; !ok {
			notes = append(notes, fmt.Sprintf("default %q is not present in the merged vaults map", reg.Default))
		}
	} else {
		fmt.Fprintln(w, "default: (none)")
	}
	fmt.Fprintln(w)
	if len(reg.Vaults) == 0 {
		fmt.Fprintln(w, "vaults: (none)")
		notes = append(notes, "registry is empty")
	} else {
		fmt.Fprintln(w, "vaults:")
		for _, name := range reg.Names() {
			conf := reg.Vaults[name]
			keys := make([]string, 0, len(conf.Config))
			for k, v := range conf.Config {
				keys = append(keys, fmt.Sprintf("%s=%v", k, v))
			}
			sort.Strings(keys)
			override := ""
			if len(conf.Overrode) > 0 {
				override = fmt.Sprintf("  (overrides %s)", strings.Join(conf.Overrode, ", "))
			}
			fmt.Fprintf(w, "  %-10s driver=%s  %s   (from %s)%s\n",
				name, conf.Driver, strings.Join(keys, " "), conf.Source, override)
			if !driverRegistered(conf.Driver) {
				notes = append(notes, fmt.Sprintf("vault %q names unregistered driver %q", name, conf.Driver))
			}
		}
	}
	for _, n := range notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	return notes
}

func driverRegistered(name string) bool {
	for _, d := range flexvault.Drivers() {
		if d == name {
			return true
		}
	}
	return false
}

// dumpRegistryYAML emits the merged registry as a plain vaults.yaml document
// (no provenance annotations).
func dumpRegistryYAML(w io.Writer, reg *vaultreg.Registry) error {
	doc := map[string]any{}
	if reg.Default != "" {
		doc["default"] = reg.Default
	}
	vaults := map[string]any{}
	for name, conf := range reg.Vaults {
		entry := map[string]any{"driver": conf.Driver}
		for k, v := range conf.Config {
			entry[k] = v
		}
		vaults[name] = entry
	}
	doc["vaults"] = vaults
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
