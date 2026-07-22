// Command flexconf is the standalone CLI for managing personal flexconf
// vaults: it mounts the flexcli `secret` group against the user's vault
// registry (~/.config/flexconf/vaults.yaml).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sylvanld/go-flexconf/flexcli"
	_ "github.com/sylvanld/go-flexconf/flexvault/driver/keepass"
)

func main() {
	opts, err := flexcli.GlobalOptions()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(flexcli.ExitFailure)
	}
	app := flexcli.New(opts)
	app.RunAgentIfRequested() // first: agent re-execs land here

	root := &cobra.Command{
		Use:           "flexconf",
		Short:         "flexconf — flexible config & secret management",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(app.SecretCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(flexcli.ExitCode(err))
	}
}
