// Command example demonstrates embedding the secretcli command tree in an
// application's own cobra CLI. It resolves the app's settings directory, then
// mounts the secret manager as the "secrets" sub-command:
//
//	example secrets set api/token s3cr3t
//	example secrets get api/token
//	example secrets list
//	example secrets agent status
//	example secrets agent lock
//
// Point it at a scratch config dir with EXAMPLE_CONFIG=/some/dir (handy for
// testing); unset, it defaults to ~/.config/example/.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sylvanld/flexconf/cli/secrets"
	"github.com/sylvanld/flexconf/settings"
)

const appName = "example"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := settings.New(appName, settings.WithPath(os.Getenv("EXAMPLE_CONFIG")))
	if err != nil {
		return err
	}
	if err := cfg.EnsureDir(); err != nil {
		return err
	}

	root := &cobra.Command{
		Use:           appName,
		Short:         "Example app embedding the flexconf secret manager",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	// Mount the reusable secret manager as "example secrets ...".
	root.AddCommand(secrets.New(cfg))

	return root.Execute()
}
