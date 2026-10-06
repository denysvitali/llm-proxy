// Package mistral implements the Mistral API used by Vibe Code and Studio.
// Chat Completions is the native wire; the server translates other client APIs.
package mistral

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

const defaultBaseURL = "https://api.mistral.ai/v1"

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
	name    string
}

func New(baseURL, key string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		HTTP:    upstream.NewClient(5 * time.Minute),
		name:    "mistral",
	}
}

func init() {
	for _, name := range []string{"mistral", "mistral-vibe"} {
		backend.Register(name, func(opts backend.Options) (backend.Backend, error) {
			client := New(opts.BaseURL, opts.APIKey)
			client.name = name
			return client, nil
		})
	}
}

func (c *Client) Name() string { return c.name }

func (c *Client) Supports(kind backend.Kind) bool { return kind == backend.KindOpenAIChat }

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, backend.Terminal(fmt.Errorf("%s backend has no API key configured", c.Name()))
	}
	if !c.Supports(req.Kind) {
		return nil, backend.Terminal(fmt.Errorf("%s backend does not support kind %q", c.Name(), req.Kind))
	}
	body, err := normalizeRequest(req.RawBody)
	if err != nil {
		return nil, backend.Terminal(fmt.Errorf("prepare Mistral request: %w", err))
	}
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+"/chat/completions", body, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	return upstream.Send(c.HTTP, httpReq, "Mistral")
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	if c.Key == "" {
		return nil, backend.Terminal(fmt.Errorf("%s backend has no API key configured", c.Name()))
	}
	req, err := upstream.CatalogRequest(ctx, c.BaseURL+"/models", c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, req, "Mistral")
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, &HTTPError{Status: resp.Status, Body: resp.Body}
	}
	return chatModelIDs(resp.Body)
}

func chatModelIDs(body []byte) ([]string, error) {
	var catalog struct {
		Data []struct {
			ID           string   `json:"id"`
			Aliases      []string `json:"aliases"`
			Archived     bool     `json:"archived"`
			Capabilities struct {
				Chat *bool `json:"completion_chat"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("decode Mistral models: %w", err)
	}
	models := make([]string, 0, len(catalog.Data))
	seen := make(map[string]bool)
	for _, model := range catalog.Data {
		if model.ID == "" || model.Archived || model.Capabilities.Chat != nil && !*model.Capabilities.Chat {
			continue
		}
		for _, id := range append([]string{model.ID}, model.Aliases...) {
			if id != "" && !seen[id] {
				models = append(models, id)
				seen[id] = true
			}
		}
	}
	return models, nil
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Mistral returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}
