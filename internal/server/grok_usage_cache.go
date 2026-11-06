package server

import (
	"context"
	"sync"
	"time"

	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
)

const (
	grokUsageBackendName = "grok"
	grokUsageTTL         = time.Minute
)

// grokUsageCache owns account usage reads and their short-lived dashboard view.
// Failed refreshes leave the previous value untouched and return the error.
type grokUsageCache struct {
	fetch func(context.Context) (grokbackend.Usage, error)
	mu    sync.Mutex
	value *grokbackend.UsageView
}

func newGrokUsageCache(manager *grokbackend.Manager, baseURL string) *grokUsageCache {
	cache := &grokUsageCache{}
	if manager != nil {
		cache.fetch = func(ctx context.Context) (grokbackend.Usage, error) {
			return manager.Usage(ctx, baseURL)
		}
	}
	return cache
}

func (c *grokUsageCache) get(ctx context.Context, refresh bool) (grokbackend.UsageView, error) {
	if c == nil || c.fetch == nil {
		return grokbackend.UsageView{}, errUsageUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.state(time.Now()) == usageSnapshotFresh {
		return cloneGrokUsage(*c.value), nil
	}
	usage, err := c.fetch(ctx)
	if err != nil {
		return grokbackend.UsageView{}, err
	}
	view := grokbackend.NewUsageView(usage, time.Now())
	c.value = &view
	return cloneGrokUsage(view), nil
}

func (c *grokUsageCache) state(now time.Time) usageSnapshotState {
	if c.value == nil {
		return usageSnapshotEmpty
	}
	return usageSnapshotStateAt(true, c.value.FetchedAt, grokUsageTTL, now)
}

// Each optional amount must remain detached while retaining unknown versus zero.
func cloneGrokUsage(view grokbackend.UsageView) grokbackend.UsageView {
	for _, amount := range []**float64{
		&view.LimitCents, &view.UsedCents, &view.RemainingCents,
		&view.OnDemandUsedCents, &view.OnDemandCapCents, &view.PrepaidCents,
	} {
		if *amount != nil {
			value := **amount
			*amount = &value
		}
	}
	return view
}
