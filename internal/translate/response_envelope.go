package translate

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// responseEnvelope pins generated buffered replies to the required Response
// fields in openai-python v1.109.1 (response.py blob a1133a41f5f661b2bb743a9d11dff1114134bf58).
// Optional settings are echoed only when the request actually supplied them.
// Service tier is deliberately excluded: requested and delivered tiers differ.
type responseEnvelope struct {
	Object               string          `json:"object"`
	CreatedAt            int64           `json:"created_at"`
	ParallelToolCalls    bool            `json:"parallel_tool_calls"`
	ToolChoice           json.RawMessage `json:"tool_choice"`
	Tools                json.RawMessage `json:"tools"`
	Instructions         json.RawMessage `json:"instructions"`
	Metadata             json.RawMessage `json:"metadata"`
	Temperature          json.RawMessage `json:"temperature"`
	TopP                 json.RawMessage `json:"top_p"`
	MaxOutputTokens      json.RawMessage `json:"max_output_tokens"`
	Reasoning            json.RawMessage `json:"reasoning,omitempty"`
	Text                 json.RawMessage `json:"text,omitempty"`
	User                 json.RawMessage `json:"user,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     json.RawMessage `json:"safety_identifier,omitempty"`
	Store                json.RawMessage `json:"store,omitempty"`
}

func newResponsesResponse(id, model string, created int64, request []byte) (responsesResponse, error) {
	if id == "" {
		id = "resp_" + rand.Text()
	}
	if created == 0 {
		created = nowUnix()
	}
	envelope := responseEnvelope{
		Object: "response", CreatedAt: created, ParallelToolCalls: true,
		ToolChoice: json.RawMessage(`"auto"`), Tools: json.RawMessage(`[]`),
	}
	if len(request) > 0 {
		if err := json.Unmarshal(request, &envelope); err != nil {
			return responsesResponse{}, err
		}
		// These are proxy/upstream facts, never client-controlled envelope fields.
		envelope.Object, envelope.CreatedAt = "response", created
		if bytes.Equal(bytes.TrimSpace(envelope.Tools), []byte("null")) {
			envelope.Tools = json.RawMessage(`[]`)
		}
		if bytes.Equal(bytes.TrimSpace(envelope.ToolChoice), []byte("null")) {
			envelope.ToolChoice = json.RawMessage(`"auto"`)
		}
	}
	return responsesResponse{
		responseEnvelope: envelope,
		ID:               id, Status: "completed", Model: model, Output: []responsesOutputItem{},
	}, nil
}

// nextItemID is independent of provider call IDs, which identify tool results
// rather than positions in the response's output array.
func (response responsesResponse) nextItemID() string {
	return fmt.Sprintf("%s_item_%d", response.ID, len(response.Output))
}

// MarshalJSON keeps required empty values and separates the output item variants.
func (item responsesOutputItem) MarshalJSON() ([]byte, error) {
	switch item.Type {
	case "message":
		content := item.Content
		if content == nil {
			content = []responsesOutputContent{}
		}
		return json.Marshal(struct {
			ID      string                   `json:"id"`
			Type    string                   `json:"type"`
			Role    string                   `json:"role"`
			Status  string                   `json:"status"`
			Content []responsesOutputContent `json:"content"`
		}{item.ID, item.Type, item.Role, item.Status, content})
	case "function_call":
		return json.Marshal(struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Status    string `json:"status,omitempty"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Namespace string `json:"namespace,omitempty"`
			Arguments string `json:"arguments"`
		}{item.ID, item.Type, item.Status, item.CallID, item.Name, item.Namespace, item.Arguments})
	case "reasoning":
		summary := item.Summary
		if summary == nil {
			summary = []responsesOutputContent{}
		}
		return json.Marshal(struct {
			ID      string                   `json:"id"`
			Type    string                   `json:"type"`
			Summary []responsesOutputContent `json:"summary"`
			Content []responsesOutputContent `json:"content,omitempty"`
		}{item.ID, item.Type, summary, item.Content})
	default:
		type raw responsesOutputItem
		return json.Marshal(raw(item))
	}
}
