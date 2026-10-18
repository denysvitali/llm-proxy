package translate

import (
	"encoding/json"
	"fmt"
)

func validateChatChoiceCount(count int) error {
	if count != 1 {
		return fmt.Errorf("translated upstream response requires exactly one choice, got %d", count)
	}
	return nil
}

func validateChatFinish(finish string) error {
	switch finish {
	case "", "stop", "length", "tool_calls", "content_filter":
		return nil
	default:
		return fmt.Errorf("unsupported upstream finish_reason %q", finish)
	}
}

func validateAnthropicStop(stop string) error {
	switch stop {
	case "", "end_turn", "stop_sequence", "max_tokens", "model_context_window_exceeded", "tool_use", "refusal":
		return nil
	default:
		return fmt.Errorf("unsupported upstream stop_reason %q", stop)
	}
}

// responsesFinish preserves the reason for incomplete generation. An absent
// status remains compatible with providers omitting it; nonterminal states do not.
func responsesFinish(response responsesResponse, hasTools bool) (string, error) {
	if response.Error != nil {
		return "", response.Error.asUpstreamError()
	}
	switch response.Status {
	case "", "completed":
		if hasTools {
			return "tool_calls", nil
		}
		return "stop", nil
	case "incomplete":
		var details struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(response.IncompleteDetails, &details); err != nil {
			return "", fmt.Errorf("invalid upstream incomplete_details: %w", err)
		}
		switch details.Reason {
		case "max_output_tokens", "model_context_window_exceeded":
			return "length", nil
		case "content_filter":
			return "content_filter", nil
		default:
			return "", fmt.Errorf("unsupported upstream incomplete reason %q", details.Reason)
		}
	default:
		return "", fmt.Errorf("upstream response status %q is not a successful terminal state", response.Status)
	}
}

func setResponsesFinish(response *responsesResponse, finish string) {
	switch finish {
	case "length":
		response.Status = "incomplete"
		response.IncompleteDetails = json.RawMessage(`{"reason":"max_output_tokens"}`)
	case "content_filter":
		response.Status = "incomplete"
		response.IncompleteDetails = json.RawMessage(`{"reason":"content_filter"}`)
	}
}

// MarshalJSON preserves required empty values without emitting fields belonging
// to a different content type.
func (part responsesOutputContent) MarshalJSON() ([]byte, error) {
	if part.Type == "refusal" {
		return json.Marshal(struct {
			Type    string `json:"type"`
			Refusal string `json:"refusal"`
		}{part.Type, part.Refusal})
	}
	if part.Type == "output_text" {
		return json.Marshal(struct {
			Type        string            `json:"type"`
			Text        string            `json:"text"`
			Annotations []json.RawMessage `json:"annotations"`
		}{part.Type, part.Text, []json.RawMessage{}})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{part.Type, part.Text})
}
