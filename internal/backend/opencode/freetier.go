package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// freeTierQuartet is the minimum OpenCode tool signature Zen's free tier
// requires in the request body. Names are matched exactly (lowercase); the
// model is never asked to call the injected copies when the caller supplied
// no tools of its own.
var freeTierQuartet = []string{"bash", "glob", "grep", "read"}

// prepareFreeTierBody rewrites a request body for Zen's free-tier gates:
// stream is forced true, and any missing {bash,glob,grep,read} tools are
// injected so the tool-signature check passes. A body that is not a JSON
// object is returned unchanged. Errors only when the body claims to be JSON
// but cannot be decoded into an object shape we can safely rewrite.
func prepareFreeTierBody(kind backend.Kind, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		// Not JSON we understand; forward as-is rather than reject the call.
		return raw, nil
	}
	body["stream"] = json.RawMessage("true")

	var tools []json.RawMessage
	hadTools := false
	if encoded, ok := body["tools"]; ok && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &tools); err != nil {
			return nil, fmt.Errorf("decode tools for free-tier rewrite: %w", err)
		}
		hadTools = true
	}

	present := make(map[string]bool, len(tools))
	for _, encoded := range tools {
		if name := toolName(kind, encoded); name != "" {
			present[name] = true
		}
	}
	var missing []string
	for _, name := range freeTierQuartet {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		for _, name := range missing {
			tools = append(tools, freeTierTool(kind, name))
		}
		encoded, err := json.Marshal(tools)
		if err != nil {
			return nil, fmt.Errorf("encode free-tier tools: %w", err)
		}
		body["tools"] = encoded
		if !hadTools {
			// The caller never offered tools; keep the injected copies inert
			// so the model does not emit calls the client cannot handle.
			choice, err := json.Marshal(freeTierToolChoiceNone(kind))
			if err != nil {
				return nil, fmt.Errorf("encode free-tier tool_choice: %w", err)
			}
			body["tool_choice"] = choice
		}
	}

	// Ask for usage on the final SSE chunk so non-stream aggregation can
	// populate token counts. Harmless if the upstream ignores it.
	if kind == backend.KindOpenAIChat {
		if _, ok := body["stream_options"]; !ok {
			body["stream_options"] = json.RawMessage(`{"include_usage":true}`)
		}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode free-tier body: %w", err)
	}
	return encoded, nil
}

func toolName(kind backend.Kind, encoded json.RawMessage) string {
	if kind == backend.KindAnthropic {
		var tool struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(encoded, &tool) == nil {
			return tool.Name
		}
		return ""
	}
	var tool struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Name string `json:"name"`
	}
	if json.Unmarshal(encoded, &tool) == nil {
		if tool.Function.Name != "" {
			return tool.Function.Name
		}
		return tool.Name
	}
	return ""
}

func freeTierToolChoiceNone(kind backend.Kind) any {
	if kind == backend.KindAnthropic {
		return map[string]any{"type": "none"}
	}
	return "none"
}

// freeTierTool returns a minimal but schema-valid definition for one of the
// quartet tools, shaped for the wire the caller is speaking.
func freeTierTool(kind backend.Kind, name string) json.RawMessage {
	desc, params := freeTierToolSpec(name)
	if kind == backend.KindAnthropic {
		encoded, err := json.Marshal(map[string]any{
			"name":         name,
			"description":  desc,
			"input_schema": params,
		})
		if err == nil {
			return encoded
		}
	} else {
		encoded, err := json.Marshal(map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": desc,
				"parameters":  params,
			},
		})
		if err == nil {
			return encoded
		}
	}
	return json.RawMessage(`{}`)
}

func freeTierToolSpec(name string) (description string, parameters map[string]any) {
	switch name {
	case "bash":
		return "Executes a given bash command in a persistent shell session with optional timeout.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The bash command to execute."},
				"workdir": map[string]any{"type": "string", "description": "Working directory for the command."},
				"timeout": map[string]any{"type": "number", "description": "Optional timeout in milliseconds."},
			},
			"required": []string{"command"},
		}
	case "glob":
		return "Fast file pattern matching tool that retrieves files matching a glob pattern.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "The glob pattern to match against."},
				"path":    map[string]any{"type": "string", "description": "Directory to search in; defaults to the current workspace."},
			},
			"required": []string{"pattern"},
		}
	case "grep":
		return "Fast content search tool that matches regex patterns across files.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "The regular expression to match."},
				"path":    map[string]any{"type": "string", "description": "Directory or file to search in."},
				"include": map[string]any{"type": "string", "description": "File filter (e.g. *.go)."},
			},
			"required": []string{"pattern"},
		}
	case "read":
		return "Reads a file from the filesystem and returns its content.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "Path to the file to read."},
				"offset":    map[string]any{"type": "number", "description": "Line number to start reading from (1-based)."},
				"limit":     map[string]any{"type": "number", "description": "Maximum number of lines to read."},
			},
			"required": []string{"file_path"},
		}
	default:
		return "OpenCode tool.", map[string]any{"type": "object", "properties": map[string]any{}}
	}
}

// aggregateFreeTierStream collapses an upstream SSE body into the non-stream
// JSON completion the client asked for. Non-2xx and non-SSE bodies are the
// caller's responsibility to forward unchanged.
func aggregateFreeTierStream(kind backend.Kind, body io.Reader) ([]byte, error) {
	if kind == backend.KindAnthropic {
		return aggregateAnthropicSSE(body)
	}
	return aggregateChatSSE(body)
}

func aggregateChatSSE(body io.Reader) ([]byte, error) {
	var id, model string
	var created float64
	var content, reasoning strings.Builder
	var calls []map[string]any
	finish := ""
	usage := map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	sawDone := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			sawDone = true
			continue
		}
		var chunk struct {
			ID      string         `json:"id"`
			Model   string         `json:"model"`
			Created float64        `json:"created"`
			Usage   map[string]any `json:"usage"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content          string           `json:"content"`
					ReasoningContent string           `json:"reasoning_content"`
					ToolCalls        []map[string]any `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return nil, fmt.Errorf("upstream stream error: %s", chunk.Error)
		}
		if chunk.ID != "" {
			id = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Created != 0 {
			created = chunk.Created
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
		content.WriteString(choice.Delta.Content)
		reasoning.WriteString(choice.Delta.ReasoningContent)
		if len(choice.Delta.ToolCalls) > 0 {
			calls = appendToolCalls(calls, choice.Delta.ToolCalls)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !sawDone && content.Len() == 0 && len(calls) == 0 && finish == "" {
		return nil, fmt.Errorf("upstream chat stream ended without a completion")
	}
	if finish == "" {
		if len(calls) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	if created == 0 {
		created = 1
	}
	return json.Marshal(map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": usage,
	})
}

func appendToolCalls(calls []map[string]any, deltas []map[string]any) []map[string]any {
	for _, delta := range deltas {
		idx := len(calls)
		if n, ok := delta["index"].(float64); ok {
			idx = int(n)
		}
		for len(calls) <= idx {
			calls = append(calls, map[string]any{
				"type":     "function",
				"id":       "",
				"function": map[string]any{"name": "", "arguments": ""},
			})
		}
		call := calls[idx]
		if v, ok := delta["id"].(string); ok && v != "" {
			call["id"] = v
		}
		if v, ok := delta["type"].(string); ok && v != "" {
			call["type"] = v
		}
		fn, _ := call["function"].(map[string]any)
		if fn == nil {
			fn = map[string]any{"name": "", "arguments": ""}
			call["function"] = fn
		}
		incoming, _ := delta["function"].(map[string]any)
		if incoming == nil {
			continue
		}
		if v, ok := incoming["name"].(string); ok && v != "" {
			fn["name"] = v
		}
		if v, ok := incoming["arguments"].(string); ok {
			fn["arguments"] = fn["arguments"].(string) + v
		}
	}
	return calls
}

func aggregateAnthropicSSE(body io.Reader) ([]byte, error) {
	out := map[string]any{
		"type":          "message",
		"role":          "assistant",
		"id":            "msg_llm_proxy",
		"content":       []any{},
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
	}
	blocks := []map[string]any{}
	stopReason := ""
	sawEvent := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			// Also accept bare JSON lines of Anthropic SSE events.
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			data = line
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID      string `json:"id"`
				Model   string `json:"model"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content"`
				Usage map[string]any `json:"usage"`
			} `json:"message"`
			Index        int `json:"index"`
			ContentBlock *struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage map[string]any `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		sawEvent = true
		switch event.Type {
		case "message_start":
			if event.Message.ID != "" {
				out["id"] = event.Message.ID
			}
			if event.Message.Model != "" {
				out["model"] = event.Message.Model
			}
			if len(event.Message.Content) > 0 {
				for _, block := range event.Message.Content {
					blocks = append(blocks, map[string]any{
						"type": block.Type,
						"text": block.Text,
					})
				}
			}
			if event.Message.Usage != nil {
				out["usage"] = event.Message.Usage
			}
		case "content_block_start":
			block := map[string]any{"type": "text", "text": ""}
			if event.ContentBlock != nil {
				block = map[string]any{"type": event.ContentBlock.Type}
				switch event.ContentBlock.Type {
				case "text", "thinking":
					block["text"] = event.ContentBlock.Text
					if event.ContentBlock.Type == "thinking" {
						block["thinking"] = event.ContentBlock.Text
						delete(block, "text")
					}
				case "tool_use":
					block["id"] = event.ContentBlock.ID
					block["name"] = event.ContentBlock.Name
					if len(event.ContentBlock.Input) > 0 {
						block["input"] = json.RawMessage(event.ContentBlock.Input)
					} else {
						block["input"] = json.RawMessage(`{}`)
					}
				}
			}
			for len(blocks) <= event.Index {
				blocks = append(blocks, map[string]any{"type": "text", "text": ""})
			}
			blocks[event.Index] = block
		case "content_block_delta":
			if event.Delta == nil {
				continue
			}
			for len(blocks) <= event.Index {
				blocks = append(blocks, map[string]any{"type": "text", "text": ""})
			}
			block := blocks[event.Index]
			switch event.Delta.Type {
			case "text_delta":
				block["text"] = block["text"].(string) + event.Delta.Text
			case "thinking_delta":
				if block["type"] == "thinking" {
					block["thinking"] = block["thinking"].(string) + event.Delta.Thinking
				} else {
					block["type"] = "thinking"
					block["thinking"] = event.Delta.Thinking
					delete(block, "text")
				}
			case "input_json_delta":
				raw, _ := block["partial"].(string)
				raw += event.Delta.PartialJSON
				block["partial"] = raw
			}
		case "content_block_stop":
			if event.Index >= 0 && event.Index < len(blocks) {
				block := blocks[event.Index]
				if partial, ok := block["partial"].(string); ok {
					delete(block, "partial")
					trimmed := strings.TrimSpace(partial)
					if trimmed == "" {
						block["input"] = json.RawMessage(`{}`)
					} else if json.Valid([]byte(trimmed)) {
						block["input"] = json.RawMessage(trimmed)
					} else {
						block["input"] = json.RawMessage(`{}`)
					}
				}
			}
		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				stopReason = event.Delta.StopReason
			}
			if event.Usage != nil {
				usage, _ := out["usage"].(map[string]any)
				if usage == nil {
					usage = map[string]any{}
					out["usage"] = usage
				}
				for k, v := range event.Usage {
					usage[k] = v
				}
			}
		case "error":
			return nil, fmt.Errorf("upstream anthropic stream error event")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !sawEvent {
		return nil, fmt.Errorf("upstream anthropic stream ended without events")
	}
	// Drop unfinished partial markers and normalize tool_use inputs.
	for i, block := range blocks {
		if partial, ok := block["partial"].(string); ok {
			delete(block, "partial")
			trimmed := strings.TrimSpace(partial)
			if json.Valid([]byte(trimmed)) && trimmed != "" {
				block["input"] = json.RawMessage(trimmed)
			} else {
				block["input"] = json.RawMessage(`{}`)
			}
			blocks[i] = block
		}
	}
	if len(blocks) == 0 {
		blocks = []map[string]any{{"type": "text", "text": ""}}
	}
	out["content"] = blocks
	if stopReason == "" {
		if hasToolUse(blocks) {
			stopReason = "tool_use"
		} else {
			stopReason = "end_turn"
		}
	}
	out["stop_reason"] = stopReason
	return json.Marshal(out)
}

func hasToolUse(blocks []map[string]any) bool {
	for _, block := range blocks {
		if block["type"] == "tool_use" {
			return true
		}
	}
	return false
}

// sseData extracts the payload from one SSE line. ok is false for blank,
// comment, and non-data lines.
func sseData(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return "", false
	}
	return payload, true
}
