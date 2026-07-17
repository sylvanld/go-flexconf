package secrets

import (
	"errors"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"forgejo.ovhcloud.tools/sylvan/flexconf/secrets"
)

func (c *cli) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "get <key>",
		Short:        "Read a secret",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			store, err := c.store()
			if err != nil {
				return err
			}
			value, err := store.GetValue(key)
			if err != nil {
				if errors.Is(err, secrets.ErrNotFound) {
					return fmt.Errorf("no secret found for key %q", key)
				}
				return fmt.Errorf("reading %q: %w", key, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), *value)
			return nil
		},
	}
}

func (c *cli) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List secrets with created/updated timestamps",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.store()
			if err != nil {
				return err
			}
			list, err := store.List()
			if err != nil {
				return err
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tCREATED\tUPDATED")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Key, formatTime(s.CreatedAt), formatTime(s.UpdatedAt))
			}
			return tw.Flush()
		},
	}
}

func (c *cli) setCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "set <key> <value>",
		Short:        "Write a secret",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.store()
			if err != nil {
				return err
			}
			if err := store.SetValue(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "stored %q\n", args[0])
			return nil
		},
	}
}

func (c *cli) deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "delete <key>",
		Short:        "Delete a secret",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			store, err := c.store()
			if err != nil {
				return err
			}
			if err := store.Delete(key); err != nil {
				if errors.Is(err, secrets.ErrNotFound) {
					return fmt.Errorf("no secret found for key %q", key)
				}
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted %q\n", key)
			return nil
		},
	}
}

// formatTime renders a timestamp for the list table, or "-" when unavailable.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
