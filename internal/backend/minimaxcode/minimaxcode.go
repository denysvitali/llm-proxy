// Package minimaxcode implements the account-backed MiniMax Code model gateway.
package minimaxcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// The MiniMax Code 0.5.5 bundle configures @ai-sdk/anthropic with this base.
// The SDK appends /v1/messages after removing the final /v1 from this URL.
const defaultBaseURL = "https://agent.minimax.io/mavis/api/v1/llm/v1"

var models = []string{"MiniMax-M3", "MiniMax-M2.7-highspeed", "MiniMax-M2.7"}

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

func (c *Client) Models(context.Context) ([]string, error) {
	return append([]string(nil), models...), nil
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
