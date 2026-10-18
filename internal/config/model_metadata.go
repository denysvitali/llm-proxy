package config

import (
	"fmt"
	"strings"
)

// ModelMetadata describes operator-verified capabilities, not request defaults.
// Entries use exact upstream IDs rather than name-based model-family guesses.
type ModelMetadata struct {
	Model                    string   `mapstructure:"model"`
	ContextWindow            *int     `mapstructure:"context_window"`
	MaxOutputTokens          *int     `mapstructure:"max_output_tokens"`
	ReasoningEfforts         []string `mapstructure:"reasoning_efforts"`
	DefaultReasoningEffort   *string  `mapstructure:"default_reasoning_effort"`
	SupportsVerbosity        bool     `mapstructure:"supports_verbosity"`
	SupportsParallelTools    bool     `mapstructure:"supports_parallel_tools"`
	SupportsReasoningSummary bool     `mapstructure:"supports_reasoning_summary"`
}

func validateModelMetadata(entries []ModelMetadata) error {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Model) == "" || entry.Model != strings.TrimSpace(entry.Model) {
			return fmt.Errorf("model_metadata requires a nonempty, unpadded model ID")
		}
		if seen[entry.Model] {
			return fmt.Errorf("model_metadata: duplicate model %q", entry.Model)
		}
		seen[entry.Model] = true
		for name, value := range map[string]*int{"context_window": entry.ContextWindow, "max_output_tokens": entry.MaxOutputTokens} {
			if value != nil && *value <= 0 {
				return fmt.Errorf("model_metadata[%s]: %s must be positive when set", entry.Model, name)
			}
		}
		if entry.ContextWindow != nil && entry.MaxOutputTokens != nil && *entry.MaxOutputTokens > *entry.ContextWindow {
			return fmt.Errorf("model_metadata[%s]: max_output_tokens exceeds context_window", entry.Model)
		}
		efforts := make(map[string]bool)
		for _, effort := range entry.ReasoningEfforts {
			switch effort {
			case "none", "minimal", "low", "medium", "high", "xhigh":
			default:
				return fmt.Errorf("model_metadata[%s]: unsupported reasoning effort %q", entry.Model, effort)
			}
			if efforts[effort] {
				return fmt.Errorf("model_metadata[%s]: duplicate reasoning effort %q", entry.Model, effort)
			}
			efforts[effort] = true
		}
		if entry.DefaultReasoningEffort != nil && !efforts[*entry.DefaultReasoningEffort] {
			return fmt.Errorf("model_metadata[%s]: default_reasoning_effort must be in reasoning_efforts", entry.Model)
		}
	}
	return nil
}
