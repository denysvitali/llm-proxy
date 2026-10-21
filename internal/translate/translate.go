// Package translate converts between the Anthropic Messages API and the
// OpenAI chat-completions API.
//
// Upstream providers expose both, but their /messages endpoints forward Anthropic
// tool definitions verbatim to OpenAI-shaped upstreams, which reject them
// ("[1210] Invalid API parameter", "tools[0].function.name is invalid or
// missing"). Requests for those models are therefore translated here and sent
// to /chat/completions instead, and the response is translated back so the
// client still speaks pure Anthropic.
package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Request is the subset of the Anthropic Messages API the proxy understands.
// Raw field presence is retained for cross-wire capability validation.
type Request struct {
	fields        map[string]json.RawMessage
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system"`
	Temperature   *float64           `json:"temperature"`
	TopP          *float64           `json:"top_p"`
	TopK          *int               `json:"top_k"`
	StopSequences []string           `json:"stop_sequences"`
	Stream        bool               `json:"stream"`
	Tools         []AnthropicTool    `json:"tools"`
	ToolChoice    json.RawMessage    `json:"tool_choice"`
	Thinking      json.RawMessage    `json:"thinking"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicBlock struct {
	Type      string           `json:"type"`
	Text      string           `json:"text"`
	Thinking  string           `json:"thinking"`
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Input     json.RawMessage  `json:"input"`
	ToolUseID string           `json:"tool_use_id"`
	Content   json.RawMessage  `json:"content"`
	IsError   bool             `json:"is_error"`
	Source    *anthropicSource `json:"source"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

type openAIRequest struct {
	Model              string          `json:"model"`
	Messages           []openAIMessage `json:"messages"`
	MaxTokens          int             `json:"max_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Stop               []string        `json:"stop,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
	Tools              []openAITool    `json:"tools,omitempty"`
	ToolChoice         any             `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool           `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort    string          `json:"reasoning_effort,omitempty"`
	StreamOptionsUsage *streamOptions  `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type openAIToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ParseRequest decodes an Anthropic Messages request body.
func ParseRequest(body []byte) (*Request, error) {
	if err := validateGenerationControls(body, "anthropic"); err != nil {
		return nil, err
	}
	var request Request
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &request.fields); err != nil {
		return nil, err
	}
	return &request, nil
}

// WantsThinking reports whether the client asked for extended thinking, which
// decides whether upstream reasoning traces are surfaced as thinking blocks.
func (r *Request) WantsThinking() bool {
	if len(r.Thinking) == 0 {
		return false
	}
	var thinking struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(r.Thinking, &thinking); err != nil {
		return false
	}
	return thinking.Type != "" && thinking.Type != "disabled"
}

// ToOpenAI renders an Anthropic request as an OpenAI chat-completions request.
func ToOpenAI(request *Request, model string) ([]byte, error) {
	if err := validateAnthropicTranslation(request, false); err != nil {
		return nil, err
	}
	if err := validateAnthropicHistory(request.Messages); err != nil {
		return nil, err
	}
	converted := openAIRequest{
		Model:       model,
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		Stop:        request.StopSequences,
		Stream:      request.Stream,
	}
	if request.Stream {
		converted.StreamOptionsUsage = &streamOptions{IncludeUsage: true}
	}
	converted.ReasoningEffort, _ = anthropicEffort(request.fields["output_config"])

	system, err := anthropicSystemContent(request.System)
	if err != nil {
		return nil, err
	}
	if system != nil {
		converted.Messages = append(converted.Messages, openAIMessage{Role: "system", Content: system})
	}
	for _, message := range request.Messages {
		translated, err := translateMessage(message)
		if err != nil {
			return nil, err
		}
		converted.Messages = append(converted.Messages, translated...)
	}
	if len(converted.Messages) == 0 {
		return nil, fmt.Errorf("request contains no messages")
	}

	for _, tool := range request.Tools {
		if tool.Name == "" {
			continue
		}
		schema, err := sanitizeSchema(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		converted.Tools = append(converted.Tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
			},
		})
	}
	converted.ToolChoice, converted.ParallelToolCalls, err = anthropicToolChoice(request.ToolChoice, false)
	if err != nil {
		return nil, err
	}

	return json.Marshal(converted)
}

// translateMessage converts one Anthropic message into the one or more OpenAI
// messages it corresponds to. Anthropic carries tool results inside a user
// message; OpenAI needs a separate "tool" message per result.
func translateMessage(message AnthropicMessage) ([]openAIMessage, error) {
	role := message.Role
	if role == "system" {
		// Claude Code places mid-conversation system messages (hook context)
		// in messages; they carry text only, like the top-level system.
		content, err := anthropicSystemContent(message.Content)
		if err != nil || content == nil {
			return nil, fmt.Errorf("system message requires text content")
		}
		return []openAIMessage{{Role: "system", Content: content}}, nil
	}
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("unsupported Messages role %q", role)
	}
	if text, ok := plainString(message.Content); ok {
		return []openAIMessage{{Role: role, Content: text}}, nil
	}
	blocks, err := anthropicInputBlocks(message.Content)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		if bytes.Equal(bytes.TrimSpace(message.Content), []byte("[]")) {
			return nil, fmt.Errorf("message content must not be an empty array")
		}
		// Only replayed thinking, which was dropped; the turn adds nothing.
		return nil, nil
	}
	var messages []openAIMessage
	var parts []any
	var calls []openAIToolCall
	flush := func() {
		if len(parts) == 0 && len(calls) == 0 {
			return
		}
		msg := openAIMessage{Role: role, ToolCalls: calls}
		if parts != nil {
			msg.Content = parts
		}
		messages = append(messages, msg)
		parts, calls = nil, nil
	}
	for _, block := range blocks {
		switch block.Type {
		case "text", "image":
			if len(calls) > 0 {
				return nil, fmt.Errorf("chat cannot preserve content following tool_use within an assistant message")
			}
			if block.Type == "text" {
				parts = append(parts, map[string]any{"type": "text", "text": block.Text})
			} else {
				if role != "user" {
					return nil, fmt.Errorf("chat cannot represent assistant images")
				}
				url, err := anthropicImageURL(block.Source)
				if err != nil {
					return nil, err
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
			}
		case "tool_use":
			if role != "assistant" {
				return nil, fmt.Errorf("tool_use requires an assistant message")
			}
			call := openAIToolCall{ID: block.ID, Type: "function"}
			call.Function.Name = block.Name
			call.Function.Arguments, err = compactJSON(block.Input)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
		case "tool_result":
			if role != "user" {
				return nil, fmt.Errorf("tool_result requires a user message")
			}
			flush()
			output, err := anthropicToolOutput(block, false)
			if err != nil {
				return nil, err
			}
			messages = append(messages, openAIMessage{Role: "tool", ToolCallID: block.ToolUseID, Content: output})
		}
	}
	flush()
	return messages, nil
}

// sanitizeSchema validates the schema envelope without changing its meaning.
// Composition keywords, dialect declarations and numeric lexemes stay intact.
func sanitizeSchema(schema json.RawMessage) (json.RawMessage, error) {
	if len(schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	trimmed := bytes.TrimSpace(schema)
	if !json.Valid(trimmed) || len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("tool parameters must be a JSON Schema object")
	}
	return schema, nil
}

// compactJSON preserves numeric lexemes and rejects non-object tool inputs.
func compactJSON(raw json.RawMessage) (string, error) {
	input, err := argumentsJSON(string(raw))
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, input); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func plainString(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, `"`) {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}
