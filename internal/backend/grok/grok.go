// Package grok implements the xAI Grok subscription backend. The
// subscription endpoint (cli-chat-proxy.grok.com) speaks the OpenAI
// Responses API. Credentials come from the signed-in xAI account session.
package grok

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

const defaultBaseURL = "https://cli-chat-proxy.grok.com/v1"

type Client struct {
	BaseURL string
	Tokens  backend.TokenSource
	HTTP    *http.Client
}

func New(baseURL string, tokens backend.TokenSource) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Tokens:  tokens,
		HTTP: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 5 * time.Minute,
			},
		},
	}
}

func (c *Client) Name() string { return "grok" }

func init() {
	backend.Register("grok", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.TokenSource), nil
	})
}

// Supports: the Grok subscription endpoint only speaks the OpenAI Responses
// API; every other inbound shape is translated by the server before Send.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindOpenAIResponses
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Tokens == nil {
		return nil, fmt.Errorf("grok backend has no xAI account configured; sign in from the dashboard")
	}
	token, err := c.Tokens.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("grok account returned an empty access token")
	}
	body, namespaces, promptCacheKey, err := normalizeRequest(req.RawBody)
	if err != nil {
		return nil, fmt.Errorf("normalize Grok request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	httpReq.Header.Set("x-authenticateresponse", "authenticate-response")
	httpReq.Header.Set("x-grok-client-version", ClientVersion)
	httpReq.Header.Set("x-grok-client-identifier", "llm-proxy")
	httpReq.Header.Set("x-grok-client-mode", "cli")
	httpReq.Header.Set("x-grok-model-override", req.Model)
	httpReq.Header.Set("x-grok-req-id", requestID())
	if promptCacheKey != "" {
		// Codex keeps prompt_cache_key stable for the lifetime of a thread.
		// Grok's subscription proxy needs the corresponding affinity headers
		// to decrypt opaque reasoning/compaction items on the next tool round.
		httpReq.Header.Set("x-grok-conv-id", promptCacheKey)
		httpReq.Header.Set("x-grok-session-id", promptCacheKey)
	}
	httpReq.Header.Set("User-Agent", "llm-proxy/"+ClientVersion)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if req.Streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Grok failed: %w", err)
	}
	bodyReader := io.ReadCloser(resp.Body)
	// Normalize every Responses response, not only namespace-tool responses.
	// Grok-compatible gateways may encode integer tool arguments as floats;
	// Codex validates the argument string against the MCP schema and rejects
	// values such as 63889.0 for an i32 field.
	bodyReader = restoreNamespaceCalls(resp.Body, namespaces, req.Streaming)
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: bodyReader}, nil
}

type namespaceTool struct {
	qualified string
	namespace string
	name      string
}

// normalizeRequest adapts Codex's grouped-tool extension to the flat function
// list accepted by Grok. Child names are qualified on the Grok wire so the
// namespace can be restored on response function_call items.
func normalizeRequest(body []byte) ([]byte, []namespaceTool, string, error) {
	normalized, err := translate.NormalizeResponsesRequest(body)
	if err != nil {
		return nil, nil, "", err
	}
	body = normalized
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, nil, "", err
	}
	var promptCacheKey string
	if raw := request["prompt_cache_key"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &promptCacheKey)
	}
	var tools []json.RawMessage
	if len(request["tools"]) == 0 {
		return body, nil, promptCacheKey, nil
	}
	if err := json.Unmarshal(request["tools"], &tools); err != nil {
		return nil, nil, "", fmt.Errorf("decode tools: %w", err)
	}

	changed := false
	flattened := make([]json.RawMessage, 0, len(tools))
	var namespaces []namespaceTool
	for _, raw := range tools {
		var header struct {
			Type  string            `json:"type"`
			Name  string            `json:"name"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, nil, "", fmt.Errorf("decode tool: %w", err)
		}
		if header.Type != "namespace" {
			flattened = append(flattened, raw)
			continue
		}
		changed = true
		for _, childRaw := range header.Tools {
			var child map[string]json.RawMessage
			if err := json.Unmarshal(childRaw, &child); err != nil {
				return nil, nil, "", fmt.Errorf("decode namespace child: %w", err)
			}
			var childName string
			if err := json.Unmarshal(child["name"], &childName); err != nil || childName == "" {
				return nil, nil, "", fmt.Errorf("namespace %q has a child without a valid name", header.Name)
			}
			qualified := qualifyToolName(header.Name, childName)
			child["name"] = json.RawMessage(mustJSON(qualified))
			encoded, err := json.Marshal(child)
			if err != nil {
				return nil, nil, "", err
			}
			flattened = append(flattened, encoded)
			namespaces = append(namespaces, namespaceTool{qualified: qualified, namespace: header.Name, name: childName})
		}
	}
	for i, raw := range flattened {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, nil, "", fmt.Errorf("decode flattened tool: %w", err)
		}
		// Codex adds this flag to web_search. Grok supports web_search itself,
		// but rejects the OpenAI-only flag as an unknown argument.
		if _, ok := tool["external_web_access"]; ok {
			delete(tool, "external_web_access")
			encoded, err := json.Marshal(tool)
			if err != nil {
				return nil, nil, "", err
			}
			flattened[i] = encoded
			changed = true
		}
	}
	if !changed {
		return body, nil, promptCacheKey, nil
	}
	encodedTools, err := json.Marshal(flattened)
	if err != nil {
		return nil, nil, "", err
	}
	request["tools"] = encodedTools
	if rawInput := request["input"]; len(rawInput) > 0 {
		var input any
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, nil, "", fmt.Errorf("decode input: %w", err)
		}
		if qualifyNamespaceValue(input) {
			encodedInput, err := json.Marshal(input)
			if err != nil {
				return nil, nil, "", err
			}
			request["input"] = encodedInput
		}
	}
	encoded, err := json.Marshal(request)
	return encoded, namespaces, promptCacheKey, err
}

func qualifyToolName(namespace, name string) string {
	if strings.HasSuffix(namespace, "__") {
		return namespace + name
	}
	return namespace + "__" + name
}

// qualifyNamespaceValue reverses response restoration when Codex sends a
// namespaced function_call back as conversation input on the next tool round.
func qualifyNamespaceValue(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "function_call" {
			name, nameOK := typed["name"].(string)
			namespace, namespaceOK := typed["namespace"].(string)
			if nameOK && namespaceOK && name != "" && namespace != "" {
				typed["name"] = qualifyToolName(namespace, name)
				delete(typed, "namespace")
				changed = true
			}
		}
		for _, child := range typed {
			if qualifyNamespaceValue(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if qualifyNamespaceValue(child) {
				changed = true
			}
		}
	}
	return changed
}

func mustJSON(value string) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func requestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func restoreNamespaceCalls(upstream io.ReadCloser, tools []namespaceTool, streaming bool) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer func() { _ = upstream.Close() }()
		var err error
		if streaming {
			err = restoreNamespaceStream(writer, upstream, tools)
		} else {
			var data []byte
			data, err = io.ReadAll(upstream)
			if err == nil {
				data = restoreNamespaceJSON(data, tools)
				_, err = writer.Write(data)
			}
		}
		_ = writer.CloseWithError(err)
	}()
	return reader
}

func restoreNamespaceStream(writer io.Writer, upstream io.Reader, tools []namespaceTool) error {
	reader := bufio.NewReaderSize(upstream, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		hasNewline := len(line) > 0 && line[len(line)-1] == '\n'
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if bytes.HasPrefix(line, []byte("data:")) {
			prefixLen := len("data:")
			for prefixLen < len(line) && (line[prefixLen] == ' ' || line[prefixLen] == '\t') {
				prefixLen++
			}
			payload := line[prefixLen:]
			if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
				line = append(append([]byte(nil), line[:prefixLen]...), restoreNamespaceJSON(payload, tools)...)
			}
		}
		if hasNewline {
			line = append(line, '\n')
		}
		if _, err := writer.Write(line); err != nil {
			return err
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func restoreNamespaceJSON(data []byte, tools []namespaceTool) []byte {
	data = translate.NormalizeResponsesToolArguments(data)
	var value any
	if json.Unmarshal(data, &value) != nil {
		return data
	}
	byName := make(map[string]namespaceTool, len(tools))
	for _, tool := range tools {
		byName[tool.qualified] = tool
	}
	if !restoreNamespaceValue(value, byName) {
		return data
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return data
	}
	return encoded
}

func restoreNamespaceValue(value any, tools map[string]namespaceTool) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "function_call" {
			if name, ok := typed["name"].(string); ok {
				if tool, found := tools[name]; found {
					typed["name"] = tool.name
					typed["namespace"] = tool.namespace
					changed = true
				}
			}
		}
		for _, child := range typed {
			if restoreNamespaceValue(child, tools) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if restoreNamespaceValue(child, tools) {
				changed = true
			}
		}
	}
	return changed
}

// Models: the subscription endpoint has no public model catalog, so a static
// list is returned instead.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	return []string{"grok-4.5", "grok-4.6", "grok-composer-2.5-fast"}, nil
}

type HTTPError struct {
	Status int
	Header http.Header
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Grok returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 5 * time.Minute}}
}
