// Package cmd implements the llm-proxy command line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is stamped at build time via -ldflags; default is a dev build.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:           "llm-proxy",
	Short:         "Proxy that speaks both the Anthropic and OpenAI APIs across multiple upstream backends",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(keysCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the llm-proxy version",
	Run: func(*cobra.Command, []string) {
		fmt.Println("llm-proxy " + version)
	},
}

// fatal prints an error and exits non-zero.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
