package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Print the merged model catalog of all enabled backends",
	RunE: func(c *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		backends, err := buildBackends(cfg)
		if err != nil {
			return err
		}
		if len(backends) == 0 {
			return fmt.Errorf("no backends configured")
		}
		ctx := c.Context()
		for _, b := range backends {
			models, err := b.Models(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: backend %s: %v\n", b.Name(), err)
				continue
			}
			sort.Strings(models)
			fmt.Printf("%s:\n", b.Name())
			for _, m := range models {
				fmt.Printf("  %s\n", m)
			}
		}
		return nil
	},
}
