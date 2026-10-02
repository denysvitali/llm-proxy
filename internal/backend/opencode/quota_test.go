package opencode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const freeQuotaError = `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Rate limit exceeded. Please try again later."},"metadata":{}}`

type quotaTransport func(*http.Request) (*http.Response, error)

func (f quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func quotaRequest(model string) *backend.Request {
	return &backend.Request{Kind: backend.KindOpenAIChat, Model: model, RawBody: []byte(`{"model":"` + model + `","messages":[]}`)}
}

func newQuotaClient(body, retryAfter string) (*Client, *atomic.Int32) {
	calls := new(atomic.Int32)
	c := New("", "test-key")
	c.HTTP = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		header := http.Header{"Content-Type": []string{"application/json"}}
		if retryAfter != "" {
			header.Set("Retry-After", retryAfter)
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return c, calls
}

func checkQuotaReply(t *testing.T, c *Client, req *backend.Request, wantBody, wantRetry string) {
	t.Helper()
	resp, err := c.Send(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.Status != http.StatusTooManyRequests || string(body) != wantBody || resp.Header.Get("Retry-After") != wantRetry {
		t.Fatalf("reply = status %d, retry %q, body %q, err %v", resp.Status, resp.Header.Get("Retry-After"), body, err)
	}
	// Caller mutations must not corrupt subsequent cached responses.
	resp.Header.Set("Retry-After", "broken")
}

func TestFreeQuotaCooldownAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, calls := newQuotaClient(freeQuotaError, "3600")
		checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, "3600")
		time.Sleep(10 * time.Second)
		req := quotaRequest("fledge-alpha-free")
		req.Streaming = true
		checkQuotaReply(t, c, req, freeQuotaError, "3590")
		if calls.Load() != 1 {
			t.Fatalf("upstream calls during cooldown = %d, want 1", calls.Load())
		}
		// Other models discover their own quota independently.
		checkQuotaReply(t, c, quotaRequest("another-free-model"), freeQuotaError, "3600")
		time.Sleep(3590 * time.Second)
		checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, "3600")
		if calls.Load() != 3 {
			t.Fatalf("upstream calls after reset = %d, want 3", calls.Load())
		}
	})
}

func TestFreeQuotaMissingRetryAfterUsesMidnightUTC(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, calls := newQuotaClient(freeQuotaError, "")
		now := time.Now()
		wait := int64(now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour).Sub(now).Seconds())
		checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, strconv.FormatInt(wait, 10))
		checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, strconv.FormatInt(wait, 10))
		if calls.Load() != 1 {
			t.Fatalf("upstream calls = %d, want 1", calls.Load())
		}
	})
}

func TestOtherRateLimitsRemainUncached(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"RateLimitError","message":"slow down"}}`,
		`{"error":{"type":"GoUsageLimitError","message":"quota"}}`,
		`not JSON`,
		strings.Repeat("x", maxQuotaBody+100),
	} {
		c, calls := newQuotaClient(body, "60")
		for range 2 {
			checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), body, "60")
		}
		if calls.Load() != 2 {
			t.Fatalf("non-daily rejection was cached: upstream calls = %d", calls.Load())
		}
	}
}

func TestFreeQuotaReset(t *testing.T) {
	now := time.Date(2026, 10, 2, 16, 30, 0, 0, time.FixedZone("local", 2*60*60))
	midnight := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Time
	}{
		{"7200", now.Add(2 * time.Hour)},
		{"0", now},
		{midnight.Format(http.TimeFormat), midnight},
		{now.Add(-time.Hour).UTC().Format(http.TimeFormat), now.Add(-time.Hour)},
		{"", midnight},
		{"invalid", midnight},
		{"-1", midnight},
		{"9223372036854775807", midnight},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := freeQuotaReset(tc.value, now); !got.Equal(tc.want) {
				t.Fatalf("reset = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFreeQuotaConcurrentReplay(t *testing.T) {
	c, calls := newQuotaClient(freeQuotaError, "86400")
	checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, "86400")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkQuotaReply(t, c, quotaRequest("fledge-alpha-free"), freeQuotaError, "86400")
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent cached calls reached upstream: %d", calls.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Send(ctx, quotaRequest("fledge-alpha-free")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cached Send = %v, want context.Canceled", err)
	}
}
