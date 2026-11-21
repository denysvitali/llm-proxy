package opendesign

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// maxMergeBody bounds how much of a non-streaming Chat response is buffered
// to merge split choices; larger bodies pass through untouched.
const maxMergeBody = 32 << 20

// mergeSplitChoices repairs Open Design's non-streaming Chat Completions
// replies, which split one assistant turn across several choices: the text in
// choices[0] and the tool calls in choices[1]. Clients that asked for a single
// completion (no n, or n=1) read only choices[0] and lose the tool calls, so
// the fragments are folded back into one choice. Streams are unaffected: they
// already carry everything on choice 0.
func mergeSplitChoices(req *backend.Request, resp *backend.Response) {
	if req.Kind != backend.KindOpenAIChat || req.Streaming || resp.Status < 200 || resp.Status >= 300 || requestsChoices(req.RawBody) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMergeBody+1))
	rest := resp.Body
	if err != nil || len(body) > maxMergeBody {
		resp.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(body), rest), Closer: rest}
		return
	}
	_ = rest.Close()
	if merged, ok := mergeChoices(body); ok {
		body = merged
		resp.Header.Del("Content-Length")
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
}

type replayBody struct {
	io.Reader
	io.Closer
}

// requestsChoices reports a request that explicitly asked for several
// completions, whose choices are genuine alternatives and must stay apart.
func requestsChoices(raw []byte) bool {
	var fields struct {
		N *int `json:"n"`
	}
	return json.Unmarshal(raw, &fields) == nil && fields.N != nil && *fields.N > 1
}

// mergeChoices folds every choice of a Chat Completions body into one, in
// index order: string contents and reasoning concatenate, tool calls and
// reasoning_details append, and the finish reason is tool_calls when any
// fragment called a tool. It declines (false) when there is nothing to merge
// or a fragment carries content it cannot combine losslessly.
func mergeChoices(body []byte) ([]byte, bool) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return nil, false
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(envelope["choices"], &choices) != nil || len(choices) < 2 {
		return nil, false
	}
	sort.SliceStable(choices, func(i, j int) bool { return choiceIndex(choices[i]) < choiceIndex(choices[j]) })

	merged := map[string]any{}
	var content, reasoningContent *string
	var toolCalls, details []json.RawMessage
	var finish string
	for _, choice := range choices {
		var message map[string]json.RawMessage
		if raw := choice["message"]; len(raw) > 0 && json.Unmarshal(raw, &message) != nil {
			return nil, false
		}
		for key, raw := range message {
			switch key {
			case "content", "reasoning_content":
				if isNull(raw) {
					continue
				}
				var text string
				if json.Unmarshal(raw, &text) != nil {
					return nil, false // typed content parts: leave the body as sent
				}
				target := &content
				if key == "reasoning_content" {
					target = &reasoningContent
				}
				if *target == nil {
					*target = new(string)
				}
				**target += text
			case "tool_calls", "reasoning_details":
				if isNull(raw) {
					continue
				}
				var items []json.RawMessage
				if json.Unmarshal(raw, &items) != nil {
					return nil, false
				}
				if key == "tool_calls" {
					toolCalls = append(toolCalls, items...)
				} else {
					details = append(details, items...)
				}
			default:
				if _, seen := merged[key]; !seen {
					merged[key] = raw
				}
			}
		}
		var reason string
		if json.Unmarshal(choice["finish_reason"], &reason) == nil && reason != "" {
			finish = reason
		}
	}
	merged["role"] = "assistant"
	if content != nil {
		merged["content"] = *content
	} else {
		merged["content"] = nil
	}
	if reasoningContent != nil {
		merged["reasoning_content"] = *reasoningContent
	}
	if len(details) > 0 {
		merged["reasoning_details"] = details
	}
	if len(toolCalls) > 0 {
		merged["tool_calls"] = reindexToolCalls(toolCalls)
		finish = "tool_calls"
	}
	choice := map[string]any{"index": 0, "message": merged}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	if raw, ok := choices[0]["logprobs"]; ok {
		choice["logprobs"] = raw
	}
	choicesJSON, err := json.Marshal([]any{choice})
	if err != nil {
		return nil, false
	}
	envelope["choices"] = choicesJSON
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, false
	}
	return out, true
}

// reindexToolCalls numbers merged tool calls from zero; each fragment counted
// its own calls, so their index fields collide or skip.
func reindexToolCalls(calls []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(calls))
	for i, raw := range calls {
		var call map[string]json.RawMessage
		if json.Unmarshal(raw, &call) != nil {
			out = append(out, raw)
			continue
		}
		if _, ok := call["index"]; ok {
			call["index"] = json.RawMessage(strconv.Itoa(i))
		}
		encoded, err := json.Marshal(call)
		if err != nil {
			out = append(out, raw)
			continue
		}
		out = append(out, encoded)
	}
	return out
}

func choiceIndex(choice map[string]json.RawMessage) int {
	var index int
	_ = json.Unmarshal(choice["index"], &index)
	return index
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
