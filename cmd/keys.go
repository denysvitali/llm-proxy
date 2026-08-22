package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/spf13/cobra"
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage proxy users and API keys",
}

var (
	keysStorePath string
)

func openStore() (*auth.Store, error) {
	path := keysStorePath
	if path == "" {
		cfg, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("load config to locate key store: %w", err)
		}
		path = cfg.Auth.File
	}
	if path == "" {
		return nil, fmt.Errorf("no key store configured: set auth.file in config or pass --store")
	}
	return auth.NewStore(path)
}

func init() {
	keysCmd.PersistentFlags().StringVar(&keysStorePath, "store", "", "path to the key store JSON (overrides auth.file)")

	keysCmd.AddCommand(&cobra.Command{
		Use:   "create-user <name>",
		Short: "Create a proxy user",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if err := store.CreateUser(args[0]); err != nil {
				return err
			}
			fmt.Printf("user %q created\n", args[0])
			return nil
		},
	})

	var keyName string
	createKey := &cobra.Command{
		Use:   "create <user>",
		Short: "Create an API key for a user (plaintext printed once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			plain, err := store.CreateKey(args[0], keyName)
			if err != nil {
				return err
			}
			fmt.Println(plain)
			fmt.Fprintln(os.Stderr, "store it now; it is not recoverable later")
			return nil
		},
	}
	createKey.Flags().StringVar(&keyName, "name", "", "human-readable label for the key")
	keysCmd.AddCommand(createKey)

	listKeys := &cobra.Command{
		Use:   "list <user>",
		Short: "List a user's API keys",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			keys, err := store.ListKeys(args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tNAME\tCREATED\tSTATUS")
			for _, k := range keys {
				status := "active"
				if k.Disabled {
					status = "disabled"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.ID, k.Name, k.CreatedAt.Format("2006-01-02"), status)
			}
			return w.Flush()
		},
	}
	keysCmd.AddCommand(listKeys)

	var disable bool
	keyState := &cobra.Command{
		Use:   "set-state <user> <key-id>",
		Short: "Enable or disable an API key",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if err := store.DisableKey(args[0], args[1], disable); err != nil {
				return err
			}
			state := "enabled"
			if disable {
				state = "disabled"
			}
			fmt.Printf("key %s %s\n", args[1], state)
			return nil
		},
	}
	keyState.Flags().BoolVar(&disable, "disable", true, "--disable (default) or --disable=false to re-enable")
	keysCmd.AddCommand(keyState)
}
