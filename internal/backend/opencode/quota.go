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

// freeQuotaState is the per-model daily free-quota latch. A model without an
// entry in Client.quotas is quotaUnarmed.
//
//	from          event                                              to
//	quotaUnarmed  429 FreeUsageLimitError with a reset in the future  quotaArmed
//	quotaArmed    same verdict again (a concurrent request)          quotaArmed (new reset)
//	quotaArmed    reset time passes                                  quotaLapsed
//	quotaLapsed   any freeQuotaResponse call sweeps the entry        quotaUnarmed
//
// Generic 429s, oversized or undecodable bodies, and resets that are not in
// the future never arm the latch. Only quotaArmed short-circuits requests.
type freeQuotaState int

const (
	quotaUnarmed freeQuotaState = iota
	quotaArmed
	quotaLapsed
)

func (s freeQuotaState) String() string {
	switch s {
	case quotaUnarmed:
		return "unarmed"
	case quotaArmed:
		return "armed"
	case quotaLapsed:
		return "lapsed"
	default:
		return fmt.Sprintf("freeQuotaState(%d)", int(s))
	}
}

// stateAt reports the latch state of a remembered verdict at now.
func (q freeQuota) stateAt(now time.Time) freeQuotaState {
	if now.Before(q.until) {
		return quotaArmed
	}
	return quotaLapsed
}

// sweepFreeQuotasLocked performs the quotaLapsed -> quotaUnarmed transition
// for every model. c.quotaMu must be held.
func (c *Client) sweepFreeQuotasLocked(now time.Time) {
	for model, quota := range c.quotas {
		if quota.stateAt(now) == quotaLapsed {
			delete(c.quotas, model)
		}
	}
}

// armFreeQuotaLocked performs the transition into quotaArmed. c.quotaMu must
// be held.
func (c *Client) armFreeQuotaLocked(model string, quota freeQuota) {
	if c.quotas == nil {
		c.quotas = make(map[string]freeQuota)
	}
	c.quotas[model] = quota
}

// freeQuotaResponse avoids sending another request for a model whose daily
// free quota is exhausted. This state is local to the client/process: another
// replica may still make its own first request to discover the same limit.
func (c *Client) freeQuotaResponse(model string) *backend.Response {
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	now := time.Now()
	c.sweepFreeQuotasLocked(now)
	quota, ok := c.quotas[model]
	if !ok || quota.stateAt(now) != quotaArmed {
		return nil
	}
	header := quota.header.Clone()
	header.Set("Retry-After", strconv.FormatInt(int64(math.Ceil(quota.until.Sub(now).Seconds())), 10))
	return &backend.Response{
		Status:    http.StatusTooManyRequests,
		Header:    header,
		Body:      io.NopCloser(bytes.NewReader(quota.body)),
		Exhausted: true,
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
	c.armFreeQuotaLocked(model, freeQuota{until: until, header: resp.Header.Clone(), body: body})
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
