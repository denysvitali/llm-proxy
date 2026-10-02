// Package upstream provides HTTP plumbing shared by inference backends.
// Providers retain ownership of authentication, endpoint selection, request
// transforms, catalog schemas, and upstream error types.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// NewClient leaves the overall request duration unlimited for streaming, while
// allowing each provider to choose how long to wait for response headers.
func NewClient(headerTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: headerTimeout,
		},
	}
}

// JSONRequest creates a POST without forwarding client headers. Providers add
// only their own authentication and protocol headers before sending it.
func JSONRequest(ctx context.Context, url string, body []byte, streaming bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if streaming {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

// Send leaves the body open for the caller to stream or consume, including
// non-success responses that the server's retry policy must inspect.
func Send(client *http.Client, req *http.Request, provider string) (*backend.Response, error) {
	resp, err := do(client, req, provider)
	if err != nil {
		return nil, err
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// CatalogRequest creates the common GET request used by bearer-token catalogs.
// Providers may add additional headers before passing it to ReadCatalog.
func CatalogRequest(ctx context.Context, url, key string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

// Catalog retains the response details needed by provider-specific decoders
// and HTTP errors. Body is bounded to 16 MiB, including on non-success status.
type Catalog struct {
	Status int
	Header http.Header
	Body   []byte
}

func ReadCatalog(client *http.Client, req *http.Request, provider string) (*Catalog, error) {
	resp, err := do(client, req, provider)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return &Catalog{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data}, nil
}

// ModelIDs decodes an OpenAI model catalog, retaining order and omitting empty
// IDs. Providers with additional catalog fields decode their own schema.
func ModelIDs(data []byte) ([]string, error) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func do(client *http.Client, req *http.Request, provider string) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", provider, err)
	}
	return resp, nil
}
