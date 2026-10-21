package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// translatedOpenAIControls handles controls independently of message conversion.
// Native routes never call it: provider extensions remain upstream-owned there.
func translatedOpenAIControls(body []byte, source, target string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage)
	present := func(name string) bool {
		raw, ok := fields[name]
		return ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	reject := func(name string) error {
		return fmt.Errorf("%s cannot be represented when translating %s to %s", name, source, target)
	}
	for name := range fields {
		if !present(name) {
			continue
		}
		supported := false
		switch name {
		case "model", "temperature", "top_p", "stream", "tools", "tool_choice", "parallel_tool_calls", "store", "metadata", "user", "safety_identifier", "service_tier", "prompt_cache_key", "prompt_cache_retention":
			supported = true
		case "include", "client_metadata":
			// Responses annotations that shape only the response or telemetry
			// (Codex sends include: ["reasoning.encrypted_content"] and a
			// client_metadata object on every turn). Neither can be produced on
			// the target wire, but the request is otherwise servable, so they
			// are dropped: a graceful loss beats failing the turn, matching how
			// backends without the annotation survive it natively.
			supported = source == "openai-responses"
		case "messages", "max_tokens", "max_completion_tokens", "n", "stop", "response_format", "reasoning_effort":
			supported = source == "openai-chat"
		case "input", "instructions", "max_output_tokens", "text", "reasoning":
			supported = source == "openai-responses"
		}
		if !supported {
			return nil, reject(name)
		}
	}
	if source == "openai-chat" {
		if err := validateChatRequestFields(fields); err != nil {
			return nil, err
		}
	}
	if present("tool_choice") {
		if err := validateOpenAIToolChoice(fields["tool_choice"], source); err != nil {
			return nil, err
		}
	}
	if present("n") {
		var n int
		if json.Unmarshal(fields["n"], &n) != nil || n != 1 {
			return nil, fmt.Errorf("translated requests support only n=1")
		}
	}
	if target == "anthropic" && present("temperature") {
		var temperature float64
		if json.Unmarshal(fields["temperature"], &temperature) != nil || temperature > 1 {
			return nil, fmt.Errorf("temperature must be at most 1 on the Messages wire")
		}
	}
	if present("stop") {
		var stops []string
		if text, ok := plainString(fields["stop"]); ok {
			stops = []string{text}
		} else if json.Unmarshal(fields["stop"], &stops) != nil || stops == nil {
			return nil, fmt.Errorf("stop must be a string or an array of strings")
		}
		if target != "anthropic" {
			return nil, reject("stop")
		}
		out["stop_sequences"] = mustMarshal(stops)
	}
	if present("parallel_tool_calls") {
		var parallel bool
		if json.Unmarshal(fields["parallel_tool_calls"], &parallel) != nil {
			return nil, fmt.Errorf("parallel_tool_calls must be a boolean")
		}
		if target == "anthropic" {
			out["disable_parallel_tool_use"] = mustMarshal(!parallel)
		} else {
			out["parallel_tool_calls"] = fields["parallel_tool_calls"]
		}
	}
	for _, name := range []string{"metadata", "user", "safety_identifier", "service_tier", "prompt_cache_key", "prompt_cache_retention"} {
		if !present(name) {
			continue
		}
		// Identity, routing, and cache hints that Codex sends on every turn
		// have no Messages counterpart and do not change what is computed,
		// so they are dropped rather than failing the request.
		if target == "anthropic" {
			continue
		}
		out[name] = fields[name]
	}
	if present("store") {
		var store bool
		if json.Unmarshal(fields["store"], &store) != nil {
			return nil, fmt.Errorf("store must be a boolean")
		}
		// store:false (always sent by Codex) is what Messages does anyway;
		// only a request to persist the response is unrepresentable.
		if target == "anthropic" && store {
			return nil, reject("store")
		}
		if target != "anthropic" {
			out["store"] = fields["store"]
		}
	}
	if present("response_format") {
		if target != "openai-responses" {
			return nil, reject("response_format")
		}
		format, err := chatFormatToResponses(fields["response_format"])
		if err != nil {
			return nil, err
		}
		out["text"] = mustMarshal(map[string]json.RawMessage{"format": format})
	}
	if present("text") {
		if target != "openai-chat" {
			return nil, reject("text")
		}
		var text struct {
			Format json.RawMessage `json:"format"`
		}
		if err := decodeContentPart(fields["text"], &text); err != nil || len(text.Format) == 0 {
			return nil, fmt.Errorf("translated text control requires only a format field")
		}
		format, err := responsesFormatToChat(text.Format)
		if err != nil {
			return nil, err
		}
		out["response_format"] = format
	}
	if present("reasoning_effort") {
		if target != "openai-responses" {
			return nil, reject("reasoning_effort")
		}
		if _, ok := plainString(fields["reasoning_effort"]); !ok {
			return nil, fmt.Errorf("reasoning_effort must be a string")
		}
		out["reasoning"] = mustMarshal(map[string]json.RawMessage{"effort": fields["reasoning_effort"]})
	}
	if present("reasoning") {
		var allowed map[string]json.RawMessage
		if err := decodeContentPart(fields["reasoning"], &allowed); err != nil || allowed == nil {
			return nil, fmt.Errorf("translated reasoning requires an object")
		}
		// Extra reasoning knobs are dropped key by key so only genuinely
		// unmappable controls fail: summary ("auto" from Codex) has no chat
		// counterpart and its absence changes nothing on the wire.
		for key := range allowed {
			switch key {
			case "effort", "summary":
			default:
				return nil, fmt.Errorf("translated reasoning supports only effort and summary; %q cannot be mapped", key)
			}
		}
		if raw, ok := allowed["effort"]; ok {
			var effort string
			if err := json.Unmarshal(raw, &effort); err != nil {
				return nil, fmt.Errorf("reasoning.effort must be a string")
			}
			// Messages has no portable effort control (output_config is not
			// accepted by every Anthropic-compatible backend, and minimal or
			// xhigh have no counterpart), so effort is a hint dropped there.
			if target == "openai-chat" {
				out["reasoning_effort"] = mustMarshal(effort)
			}
		}
	}
	return out, nil
}

func validateOpenAIToolChoice(raw json.RawMessage, source string) error {
	if choice, ok := plainString(raw); ok {
		switch choice {
		case "auto", "none", "required":
			return nil
		default:
			return fmt.Errorf("unsupported tool_choice %q", choice)
		}
	}
	var choice struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Function  *struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := decodeContentPart(raw, &choice); err != nil || choice.Type != "function" {
		return fmt.Errorf("translated tool_choice requires auto, none, required, or a named function")
	}
	if source == "openai-chat" {
		if choice.Function == nil || choice.Function.Name == "" || choice.Name != "" || choice.Namespace != "" {
			return fmt.Errorf("chat tool_choice requires function.name")
		}
	} else if choice.Name == "" || choice.Function != nil {
		return fmt.Errorf("responses tool_choice requires name")
	}
	return nil
}

func marshalWithOpenAIControls(request any, controls map[string]json.RawMessage) ([]byte, error) {
	body, err := json.Marshal(request)
	if err != nil || len(controls) == 0 {
		return body, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	for key, value := range controls {
		if key == "disable_parallel_tool_use" {
			choice := make(map[string]json.RawMessage)
			if raw := fields["tool_choice"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &choice); err != nil {
					return nil, err
				}
			} else {
				choice["type"] = mustMarshal("auto")
			}
			// The none choice already prohibits every tool call; it does not
			// accept the parallel flag on the Messages wire.
			if string(choice["type"]) != `"none"` {
				choice[key] = value
			}
			fields["tool_choice"] = mustMarshal(choice)
			continue
		}
		fields[key] = value
	}
	return json.Marshal(fields)
}

func chatFormatToResponses(raw json.RawMessage) (json.RawMessage, error) {
	var format struct {
		Type   string          `json:"type"`
		Schema json.RawMessage `json:"json_schema"`
	}
	if err := decodeContentPart(raw, &format); err != nil {
		return nil, fmt.Errorf("unsupported response_format: %w", err)
	}
	switch format.Type {
	case "text", "json_object":
		if len(format.Schema) > 0 {
			return nil, fmt.Errorf("json_schema requires response_format type json_schema")
		}
		return raw, nil
	case "json_schema":
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(format.Schema, &schema); err != nil || schema == nil {
			return nil, fmt.Errorf("json_schema must be an object")
		}
		if err := validateOutputSchema(schema); err != nil {
			return nil, err
		}
		schema["type"] = mustMarshal("json_schema")
		return json.Marshal(schema)
	default:
		return nil, fmt.Errorf("unsupported response_format type %q", format.Type)
	}
}

func responsesFormatToChat(raw json.RawMessage) (json.RawMessage, error) {
	var format map[string]json.RawMessage
	if err := json.Unmarshal(raw, &format); err != nil || format == nil {
		return nil, fmt.Errorf("text.format must be an object")
	}
	var kind string
	if err := json.Unmarshal(format["type"], &kind); err != nil {
		return nil, fmt.Errorf("text.format requires a type")
	}
	if kind == "text" || kind == "json_object" {
		if len(format) != 1 {
			return nil, fmt.Errorf("unsupported text.format fields")
		}
		return raw, nil
	}
	if kind != "json_schema" {
		return nil, fmt.Errorf("unsupported text.format type %q", kind)
	}
	delete(format, "type")
	if err := validateOutputSchema(format); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"type": kind, "json_schema": format})
}

func validateOutputSchema(fields map[string]json.RawMessage) error {
	for name := range fields {
		switch name {
		case "name", "description", "schema", "strict":
		default:
			return fmt.Errorf("unsupported output schema field %q", name)
		}
	}
	if name, ok := plainString(fields["name"]); !ok || name == "" {
		return fmt.Errorf("output schema requires a non-empty name")
	}
	if len(fields["schema"]) == 0 {
		return fmt.Errorf("output schema requires schema")
	}
	if _, err := sanitizeSchema(fields["schema"]); err != nil {
		return err
	}
	if raw, ok := fields["strict"]; ok {
		var strict bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &strict) != nil {
			return fmt.Errorf("output schema strict must be a boolean")
		}
	}
	return nil
}
