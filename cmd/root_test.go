package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestExecuteNoArgs(t *testing.T) {
	rootCmd.SetArgs(nil)
	oldOut, oldErr := os.Stdout, os.Stderr
	var so, se bytes.Buffer
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&so, rOut)
		_, _ = io.Copy(&se, rErr)
		close(done)
	}()

	if err := Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_ = wOut.Close()
	_ = wErr.Close()
	<-done
	os.Stdout, os.Stderr = oldOut, oldErr

	out := so.String()
	if !strings.Contains(out, "Proxy that speaks both the Anthropic and OpenAI APIs") {
		t.Errorf("Execute() output = %q, want help text", out)
	}
	if got := se.String(); got != "" {
		t.Errorf("Execute() wrote to stderr = %q, want empty", got)
	}
}

func TestVersionCommand(t *testing.T) {
	rootCmd.SetArgs(nil)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	if got := strings.TrimSpace(string(out)); got != "llm-proxy dev" {
		t.Errorf("version output = %q, want %q", got, "llm-proxy dev")
	}
}

func TestMiniMaxAutoCheckinEnabled(t *testing.T) {
	no := false
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{"absent backend", config.Config{}, false},
		{"enabled by default", config.Config{Backends: []config.BackendConfig{{Type: "minimax-code"}}}, true},
		{"disabled backend", config.Config{Backends: []config.BackendConfig{{Type: "minimax-code", Enabled: &no}}}, false},
		{"opt out", config.Config{Backends: []config.BackendConfig{{Type: "minimax-code"}}, MiniMaxCodeAutoCheckin: &no}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := minimaxAutoCheckinEnabled(&tc.cfg); got != tc.want {
				t.Fatalf("enabled = %v, want %v", got, tc.want)
			}
		})
	}
}
