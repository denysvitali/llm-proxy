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

// responsesTerminalKind classifies a successful Responses terminal state.
// The classification is total over the accepted inputs; everything else is an
// error rather than a kind:
//
//	response.error set                                     -> upstream error
//	status "" | "completed"                                -> responsesTerminalCompleted
//	status "incomplete", reason max_output_tokens |
//	    model_context_window_exceeded                      -> responsesTerminalLength
//	status "incomplete", reason content_filter             -> responsesTerminalContentFilter
//	any other status or incomplete reason                  -> error
type responsesTerminalKind int

const (
	responsesTerminalCompleted responsesTerminalKind = iota
	responsesTerminalLength
	responsesTerminalContentFilter
)

func (k responsesTerminalKind) String() string {
	switch k {
	case responsesTerminalCompleted:
		return "completed"
	case responsesTerminalLength:
		return "length"
	case responsesTerminalContentFilter:
		return "content_filter"
	default:
		return fmt.Sprintf("responsesTerminalKind(%d)", int(k))
	}
}

// chatFinish maps the terminal kind to a Chat Completions finish_reason.
func (k responsesTerminalKind) chatFinish(hasTools bool) string {
	switch k {
	case responsesTerminalLength:
		return "length"
	case responsesTerminalContentFilter:
		return "content_filter"
	default:
		if hasTools {
			return "tool_calls"
		}
		return "stop"
	}
}

// responsesTerminal classifies a terminal response. An absent status remains
// compatible with providers omitting it; nonterminal states do not.
func responsesTerminal(response responsesResponse) (responsesTerminalKind, error) {
	if response.Error != nil {
		return 0, response.Error.asUpstreamError()
	}
	switch response.Status {
	case "", "completed":
		return responsesTerminalCompleted, nil
	case "incomplete":
		var details struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(response.IncompleteDetails, &details); err != nil {
			return 0, fmt.Errorf("invalid upstream incomplete_details: %w", err)
		}
		switch details.Reason {
		case "max_output_tokens", "model_context_window_exceeded":
			return responsesTerminalLength, nil
		case "content_filter":
			return responsesTerminalContentFilter, nil
		default:
			return 0, fmt.Errorf("unsupported upstream incomplete reason %q", details.Reason)
		}
	default:
		return 0, fmt.Errorf("upstream response status %q is not a successful terminal state", response.Status)
	}
}

// responsesFinish preserves the reason for incomplete generation as a Chat
// finish_reason; see responsesTerminal for the classification.
func responsesFinish(response responsesResponse, hasTools bool) (string, error) {
	kind, err := responsesTerminal(response)
	if err != nil {
		return "", err
	}
	return kind.chatFinish(hasTools), nil
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
