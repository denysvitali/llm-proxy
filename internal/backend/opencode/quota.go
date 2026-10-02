package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const maxQuotaBody = 64 << 10

type freeQuota struct {
	until  time.Time
	header http.Header
	body   []byte
}

// freeQuotaResponse avoids sending another request for a model whose daily
// free quota is exhausted. This state is local to the client/process: another
// replica may still make its own first request to discover the same limit.
func (c *Client) freeQuotaResponse(model string) *backend.Response {
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	now := time.Now()
	for model, quota := range c.quotas {
		if !now.Before(quota.until) {
			delete(c.quotas, model)
		}
	}
	quota, ok := c.quotas[model]
	if !ok {
		return nil
	}
	header := quota.header.Clone()
	header.Set("Retry-After", strconv.FormatInt(int64(math.Ceil(quota.until.Sub(now).Seconds())), 10))
	return &backend.Response{
		Status: http.StatusTooManyRequests,
		Header: header,
		Body:   io.NopCloser(bytes.NewReader(quota.body)),
	}
}

type quotaReplayBody struct {
	io.Reader
	io.Closer
}

// rememberFreeQuota only remembers OpenCode's explicit daily-quota verdict.
// Generic 429s, authentication failures and overloads keep their normal path.
// The bounded read is replayed even when the body is too large to cache.
func (c *Client) rememberFreeQuota(model string, resp *http.Response) error {
	if resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQuotaBody+1))
	if err != nil {
		_ = resp.Body.Close()
		return backend.Terminal(fmt.Errorf("read OpenCode quota response: %w", err))
	}
	resp.Body = &quotaReplayBody{Reader: io.MultiReader(bytes.NewReader(body), resp.Body), Closer: resp.Body}
	var verdict struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if len(body) > maxQuotaBody || json.Unmarshal(body, &verdict) != nil || verdict.Error.Type != "FreeUsageLimitError" {
		return nil
	}
	now := time.Now()
	until := freeQuotaReset(resp.Header.Get("Retry-After"), now)
	if !until.After(now) {
		return nil
	}
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	// Zen's FreeUsageLimitError resets at midnight UTC. Supply that guidance
	// when an intermediary omitted or damaged the provider's Retry-After.
	resp.Header.Set("Retry-After", strconv.FormatInt(int64(math.Ceil(until.Sub(now).Seconds())), 10))
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	if c.quotas == nil {
		c.quotas = make(map[string]freeQuota)
	}
	c.quotas[model] = freeQuota{until: until, header: resp.Header.Clone(), body: body}
	return nil
}

func freeQuotaReset(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil {
		return date
	}
	return now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
}
