// llm-proxy speaks both the Anthropic Messages API and the OpenAI APIs
// (Chat Completions, Responses), authenticates proxy users with API keys,
// and routes to upstream backends (OpenCode Zen, Grok, Venice, Nous, Apodex).
package main

import (
	"os"

	"github.com/denysvitali/llm-proxy/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
