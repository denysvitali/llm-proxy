// Package workbuddy exposes the models included with a signed-in WorkBuddy
// desktop account. The private service speaks streaming OpenAI Chat only.
package workbuddy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	defaultBaseURL = "https://www.codebuddy.ai"
	clientVersion  = "2.110.0"
)

const modelCacheTTL = 5 * time.Minute

type Client struct {
	BaseURL string
	Tokens  backend.TokenSource
	HTTP    *http.Client

	modelMu      sync.Mutex
	cachedModels []string
	modelCredits map[string]string
	modelsUntil  time.Time
}

func New(baseURL string, tokens backend.TokenSource) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Tokens: tokens, HTTP: &http.Client{Timeout: 0, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 5 * time.Minute}}}
}

func init() {
	backend.Register("workbuddy", func(opts backend.Options) (backend.Backend, error) { return New(opts.BaseURL, opts.TokenSource), nil })
}
func (c *Client) Name() string                    { return "workbuddy" }
func (c *Client) Supports(kind backend.Kind) bool { return kind == backend.KindOpenAIChat }

func (c *Client) Models(ctx context.Context) ([]string, error) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if len(c.cachedModels) > 0 && time.Now().Before(c.modelsUntil) {
		return append([]string(nil), c.cachedModels...), nil
	}
	models, credits, err := c.fetchModels(ctx)
	if err != nil {
		if len(c.cachedModels) > 0 {
			return append([]string(nil), c.cachedModels...), nil
		}
		return nil, err
	}
	c.cachedModels = append(c.cachedModels[:0], models...)
	c.modelCredits = credits
	c.modelsUntil = time.Now().Add(modelCacheTTL)
	return append([]string(nil), models...), nil
}

// ModelCredits returns the credit-rate labels attached to the most recently
// fetched live catalog. Call Models first to refresh the catalog.
func (c *Client) ModelCredits() map[string]string {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	out := make(map[string]string, len(c.modelCredits))
	for model, credits := range c.modelCredits {
		out[model] = credits
	}
	return out
}

func (c *Client) fetchModels(ctx context.Context) ([]string, map[string]string, error) {
	if c.Tokens == nil {
		return nil, nil, errors.New("workbuddy backend has no account session configured")
	}
	credentials, token, err := c.credentials(ctx)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v3/config", nil)
	if err != nil {
		return nil, nil, err
	}
	setHeaders(req.Header, token, c.BaseURL)
	req.Header.Set("Accept", "application/json")
	if credentials.AccessToken != "" {
		setAccountHeaders(req.Header, credentials)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch CodeBuddy model catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("fetch CodeBuddy model catalog: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Models []struct {
				ID      string   `json:"id"`
				Tags    []string `json:"tags"`
				Credits string   `json:"credits"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("decode CodeBuddy model catalog: %w", err)
	}
	if payload.Code != 0 {
		return nil, nil, fmt.Errorf("fetch CodeBuddy model catalog: code %d: %s", payload.Code, payload.Msg)
	}
	models := make([]string, 0, len(payload.Data.Models))
	credits := make(map[string]string, len(payload.Data.Models))
	for _, model := range payload.Data.Models {
		if model.ID != "" && !mediaModel(model.Tags) {
			models = append(models, model.ID)
			if model.Credits != "" {
				credits[model.ID] = model.Credits
			}
		}
	}
	if len(models) == 0 {
		return nil, nil, errors.New("CodeBuddy model catalog is empty")
	}
	return models, credits, nil
}

func mediaModel(tags []string) bool {
	for _, tag := range tags {
		if strings.Contains(tag, "image") || strings.Contains(tag, "video") {
			return true
		}
	}
	return false
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Tokens == nil {
		return nil, errors.New("workbuddy backend has no account session configured")
	}
	credentials, token, err := c.credentials(ctx)
	if err != nil {
		return nil, err
	}
	body, err := normalizeRequest(req.RawBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v2/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setHeaders(httpReq.Header, token, c.BaseURL)
	if credentials.AccessToken != "" {
		setAccountHeaders(httpReq.Header, credentials)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to WorkBuddy failed: %w", err)
	}
	if req.Streaming || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
	}
	aggregated, err := aggregateSSE(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("decode WorkBuddy stream: %w", err)
	}
	header := resp.Header.Clone()
	header.Set("Content-Type", "application/json")
	return &backend.Response{Status: resp.StatusCode, Header: header, Body: io.NopCloser(bytes.NewReader(aggregated))}, nil
}

func (c *Client) credentials(ctx context.Context) (Credentials, string, error) {
	if source, ok := c.Tokens.(interface {
		Credentials(context.Context) (Credentials, error)
	}); ok {
		credentials, err := source.Credentials(ctx)
		return credentials, credentials.AccessToken, err
	}
	token, err := c.Tokens.AccessToken(ctx)
	return Credentials{}, token, err
}

func normalizeRequest(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode WorkBuddy request: %w", err)
	}
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	if messages, ok := body["messages"].([]any); ok {
		if len(messages) == 0 || messageRole(messages[0]) != "system" {
			body["messages"] = append([]any{map[string]any{"role": "system", "content": "You are a helpful coding assistant."}}, messages...)
		}
		for _, value := range body["messages"].([]any) {
			message, _ := value.(map[string]any)
			if messageRole(message) == "system" {
				message["content"] = sanitizeChannelIdentity(message["content"])
			}
		}
	}
	if _, ok := body["tool_choice"].(map[string]any); ok {
		body["tool_choice"] = "auto"
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, item := range tools {
			if tool, ok := item.(map[string]any); ok {
				if fn, ok := tool["function"].(map[string]any); ok {
					if schema, ok := fn["parameters"].(map[string]any); ok {
						sanitizeSchema(schema)
					}
				}
			}
		}
	}
	return json.Marshal(body)
}

func sanitizeChannelIdentity(content any) any {
	switch value := content.(type) {
	case string:
		lines := strings.Split(value, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "x-anthropic-billing-header:") {
				continue
			}
			kept = append(kept, line)
		}
		text := strings.Join(kept, "\n")
		return strings.NewReplacer(
			"Claude Agent SDK", "agent SDK",
			"Claude Code", "coding assistant",
			"Claude", "assistant",
			"Anthropic", "the model provider",
			"Codex CLI", "coding CLI",
			"Codex", "coding agent",
			"OpenAI", "the model provider",
		).Replace(text)
	case []any:
		for _, item := range value {
			block, _ := item.(map[string]any)
			if text, ok := block["text"].(string); ok {
				block["text"] = sanitizeChannelIdentity(text)
			}
		}
		return value
	default:
		return content
	}
}

func messageRole(value any) string {
	message, _ := value.(map[string]any)
	role, _ := message["role"].(string)
	return role
}

func sanitizeSchema(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "$schema")
		delete(x, "const")
		if choices, ok := x["anyOf"].([]any); ok && len(choices) > 0 {
			delete(x, "anyOf")
			if first, ok := choices[0].(map[string]any); ok {
				for k, value := range first {
					if _, exists := x[k]; !exists {
						x[k] = value
					}
				}
			}
		}
		for _, child := range x {
			sanitizeSchema(child)
		}
	case []any:
		for _, child := range x {
			sanitizeSchema(child)
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func setHeaders(h http.Header, token, baseURL string) {
	trace, span, parent := randomHex(32), randomHex(16), randomHex(16)
	h.Set("Authorization", "Bearer "+token)
	h.Set("X-API-Key", token)
	suffix := token
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	h.Set("X-User-Id", "anonymous_"+suffix)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("X-Agent-Intent", "craft")
	h.Set("X-Agent-Purpose", "conversation")
	h.Set("X-IDE-Type", "CLI")
	h.Set("X-IDE-Name", "CLI")
	h.Set("X-IDE-Version", clientVersion)
	h.Set("X-Product", "SaaS")
	h.Set("X-Private-Data", "false")
	h.Set("X-Conversation-ID", randomUUID())
	h.Set("X-Conversation-Request-ID", randomHex(32))
	h.Set("X-Conversation-Message-ID", randomHex(32))
	h.Set("X-Request-ID", trace)
	h.Set("X-Trace-ID", trace)
	h.Set("B3", trace+"-"+span+"-1-"+parent)
	h.Set("X-B3-TraceId", trace)
	h.Set("X-B3-SpanId", span)
	h.Set("X-B3-ParentSpanId", parent)
	h.Set("X-B3-Sampled", "1")
	h.Set("X-Stainless-Arch", runtime.GOARCH)
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-OS", runtime.GOOS)
	h.Set("User-Agent", "CLI/"+clientVersion+" CodeBuddy/"+clientVersion)
	origin := strings.TrimRight(baseURL, "/")
	h.Set("Origin", origin)
	h.Set("Referer", origin+"/")
}

func setAccountHeaders(h http.Header, credentials Credentials) {
	h.Del("X-API-Key")
	if credentials.UserID != "" {
		h.Set("X-User-Id", credentials.UserID)
	}
	if credentials.EnterpriseID != "" {
		h.Set("X-Enterprise-Id", credentials.EnterpriseID)
	}
	if credentials.RefreshToken != "" {
		h.Set("X-Refresh-Token", credentials.RefreshToken)
	}
	if credentials.Domain != "" {
		h.Set("X-Domain", credentials.Domain)
	}
}
func randomUUID() string {
	x := randomHex(32)
	return x[:8] + "-" + x[8:12] + "-4" + x[13:16] + "-a" + x[17:20] + "-" + x[20:]
}

func aggregateSSE(r io.Reader) ([]byte, error) {
	var id, model string
	var created float64
	var content, reasoning strings.Builder
	var calls []map[string]any
	finish := "stop"
	usage := map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 4<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if v, ok := chunk["id"].(string); ok {
			id = v
		}
		if v, ok := chunk["model"].(string); ok {
			model = v
		}
		if v, ok := chunk["created"].(float64); ok {
			created = v
		}
		if v, ok := chunk["usage"].(map[string]any); ok {
			usage = v
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if v, ok := choice["finish_reason"].(string); ok && v != "" {
			finish = v
		}
		delta, _ := choice["delta"].(map[string]any)
		if v, ok := delta["content"].(string); ok {
			content.WriteString(v)
		}
		if v, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(v)
		}
		if values, ok := delta["tool_calls"].([]any); ok {
			calls = appendToolDeltas(calls, values)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	return json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
}

func appendToolDeltas(calls []map[string]any, values []any) []map[string]any {
	for _, raw := range values {
		d, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		idx := len(calls)
		if n, ok := d["index"].(float64); ok {
			idx = int(n)
		}
		for len(calls) <= idx {
			calls = append(calls, map[string]any{"type": "function", "function": map[string]any{"name": "", "arguments": ""}})
		}
		call := calls[idx]
		if v, ok := d["id"].(string); ok {
			call["id"] = v
		}
		fn, _ := call["function"].(map[string]any)
		incoming, _ := d["function"].(map[string]any)
		if v, ok := incoming["name"].(string); ok {
			fn["name"] = fn["name"].(string) + v
		}
		if v, ok := incoming["arguments"].(string); ok {
			fn["arguments"] = fn["arguments"].(string) + v
		}
	}
	return calls
}
