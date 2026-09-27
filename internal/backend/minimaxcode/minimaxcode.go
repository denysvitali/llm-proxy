// Package minimaxcode implements the account-backed MiniMax Code model gateway.
package minimaxcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// The MiniMax Code 0.5.5 bundle configures @ai-sdk/anthropic with this base.
// The SDK appends /v1/messages after removing the final /v1 from this URL.
const defaultBaseURL = "https://agent.minimax.io/mavis/api/v1/llm/v1"

// catalogPath is the official model snapshot MiniMax Code refreshes before
// opening its picker (BKe in @minimax-ai/code 0.5.5). It is served by the
// gateway origin, not under the inference /llm/v1 prefix.
const catalogPath = "/mavis/api/v1/models"

// fallbackModels are the IDs baked into MiniMax Code 0.5.5. The live snapshot
// adds models (MiniMax-M3.1-Flash-Preview landed after that release), so these
// are only used when the snapshot cannot be fetched or parsed.
var fallbackModels = []string{"MiniMax-M3", "MiniMax-M2.7-highspeed", "MiniMax-M2.7"}

const catalogResponseLimit = 1 << 20

type Client struct {
	BaseURL string
	Key     string
	Tokens  backend.TokenSource
	HTTP    *http.Client
}

func New(baseURL, key string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     strings.TrimSpace(key),
		HTTP: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 10 * time.Minute,
		}},
	}
}

func init() {
	backend.Register("minimax-code", func(opts backend.Options) (backend.Backend, error) {
		client := New(opts.BaseURL, opts.APIKey)
		client.Tokens = opts.TokenSource
		return client, nil
	})
}

func (c *Client) Name() string { return "minimax-code" }

func (c *Client) Supports(kind backend.Kind) bool { return kind == backend.KindAnthropic }

func (c *Client) Models(ctx context.Context) ([]string, error) {
	models, err := c.fetchModels(ctx)
	if err != nil || len(models) == 0 {
		return append([]string(nil), fallbackModels...), nil
	}
	return models, nil
}

// fetchModels reads the official model snapshot. MiniMax Code requests it as
// <gateway origin>/mavis/api/v1/models?region=en&buildEnv=prod and copies every
// model of the "minimax" provider, ordered by model_order. The snapshot's
// whitelist field is ignored by the client, so it is ignored here too.
func (c *Client) fetchModels(ctx context.Context) ([]string, error) {
	endpoint, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = catalogPath
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{"region": {"en"}, "buildEnv": {"prod"}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.Key); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogResponseLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MiniMax Code model catalog returned %d", resp.StatusCode)
	}
	return parseModelCatalog(body)
}

// catalogSnapshot is the subset of the official model config this proxy needs.
// Extra provider fields are intentionally dropped.
type catalogSnapshot struct {
	Providers []struct {
		ProviderID string `json:"providerId"`
		Config     struct {
			Models     map[string]json.RawMessage `json:"models"`
			ModelOrder []string                   `json:"model_order"`
		} `json:"config"`
	} `json:"providers"`
}

func parseModelCatalog(body []byte) ([]string, error) {
	var snapshot catalogSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("parse MiniMax Code model catalog: %w", err)
	}
	var config *struct {
		Models     map[string]json.RawMessage `json:"models"`
		ModelOrder []string                   `json:"model_order"`
	}
	for i := range snapshot.Providers {
		if snapshot.Providers[i].ProviderID == "minimax" {
			config = &snapshot.Providers[i].Config
			break
		}
	}
	if config == nil || len(config.Models) == 0 {
		return nil, fmt.Errorf("MiniMax Code model catalog has no minimax models")
	}
	seen := make(map[string]bool, len(config.Models))
	models := make([]string, 0, len(config.Models))
	for _, id := range config.ModelOrder {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := config.Models[id]; !ok {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	for id := range config.Models {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("MiniMax Code model catalog has no minimax models")
	}
	return models, nil
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if !c.Supports(req.Kind) {
		return nil, fmt.Errorf("MiniMax Code backend does not support kind %q", req.Kind)
	}
	token := c.Key
	if c.Tokens != nil {
		var err error
		token, err = c.Tokens.AccessToken(ctx)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("MiniMax Code account is not signed in")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/messages", bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	// The managed provider uses a placeholder SDK key and a separate OAuth bearer.
	httpReq.Header.Set("X-Api-Key", "sk-xxx")
	httpReq.Header.Set("Anthropic-Version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	httpReq.Header.Set("User-Agent", "MiniMaxAgent")
	httpReq.Header.Set("X-Mavis-Agent-Id", "main")
	httpReq.Header.Set("X-Mavis-Timezone-Offset", "0")
	// Preserve a caller's conversation identity without disclosing its raw ID.
	session := req.Header.Get("X-Mavis-Session-Id")
	if session == "" {
		session = req.Header.Get("X-Session-Id")
	}
	if session == "" {
		session = req.Header.Get("X-OpenCode-Session")
	}
	var id [16]byte
	if session == "" {
		if _, err := rand.Read(id[:]); err != nil {
			return nil, fmt.Errorf("generate MiniMax Code session ID: %w", err)
		}
	} else {
		sum := sha256.Sum256([]byte(token + "\x00" + session))
		copy(id[:], sum[:16])
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	hexID := hex.EncodeToString(id[:])
	httpReq.Header.Set("X-Mavis-Session-Id", hexID[:8]+"-"+hexID[8:12]+"-"+hexID[12:16]+"-"+hexID[16:20]+"-"+hexID[20:])

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to MiniMax Code failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}
