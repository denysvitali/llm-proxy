package server

import (
	"context"
	"sync"
	"time"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

const (
	zcodeUsageBackendName = "zcode"
	zcodeUsageTTL         = time.Minute
)

// ZCode exposes plans and quota separately. Each read keeps its own freshness
// window and lock, while claims invalidate both cached billing snapshots.
type zcodeUsageCache struct {
	fetchPlans func(context.Context) ([]zcodebackend.PlanUsage, error)
	fetchQuota func(context.Context) (zcodebackend.PlanQuota, error)

	plansMu    sync.Mutex
	plansValue []zcodebackend.PlanUsage
	plansAt    time.Time
	quotaMu    sync.Mutex
	quotaValue zcodebackend.PlanQuota
	quotaAt    time.Time
}

func newZcodeUsageCache(manager *zcodebackend.Manager) *zcodeUsageCache {
	cache := &zcodeUsageCache{}
	if manager != nil {
		cache.fetchPlans = manager.PlanUsage
		cache.fetchQuota = manager.PlanQuota
	}
	return cache
}

func (c *zcodeUsageCache) plans(ctx context.Context) ([]zcodebackend.PlanUsage, error) {
	if c == nil || c.fetchPlans == nil {
		return nil, errZcodeUsageUnavailable
	}
	c.plansMu.Lock()
	defer c.plansMu.Unlock()
	if !c.plansAt.IsZero() && time.Since(c.plansAt) < zcodeUsageTTL {
		return cloneZcodePlans(c.plansValue), nil
	}
	plans, err := c.fetchPlans(ctx)
	if err != nil {
		return nil, err
	}
	c.plansValue = cloneZcodePlans(plans)
	c.plansAt = time.Now()
	return cloneZcodePlans(c.plansValue), nil
}

func (c *zcodeUsageCache) quota(ctx context.Context) (zcodebackend.PlanQuota, error) {
	if c == nil || c.fetchQuota == nil {
		return zcodebackend.PlanQuota{}, errZcodeQuotaUnavailable
	}
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	if !c.quotaAt.IsZero() && time.Since(c.quotaAt) < zcodeUsageTTL {
		return cloneZcodeQuota(c.quotaValue), nil
	}
	quota, err := c.fetchQuota(ctx)
	if err != nil {
		return zcodebackend.PlanQuota{}, err
	}
	c.quotaValue = cloneZcodeQuota(quota)
	c.quotaAt = time.Now()
	return cloneZcodeQuota(c.quotaValue), nil
}

func (c *zcodeUsageCache) invalidate() {
	if c == nil {
		return
	}
	// Waiting for in-flight reads before clearing timestamps prevents a
	// pre-claim fetch from making its old snapshot fresh after invalidation.
	c.plansMu.Lock()
	defer c.plansMu.Unlock()
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	c.plansAt = time.Time{}
	c.quotaAt = time.Time{}
}

func cloneZcodePlans(plans []zcodebackend.PlanUsage) []zcodebackend.PlanUsage {
	// Preserve the existing API representation: no plans serializes as null.
	cloned := append([]zcodebackend.PlanUsage(nil), plans...)
	for i := range cloned {
		cloned[i].Entitlements = append([]zcodebackend.PlanEntitlement(nil), plans[i].Entitlements...)
	}
	return cloned
}

func cloneZcodeQuota(quota zcodebackend.PlanQuota) zcodebackend.PlanQuota {
	cloned := zcodebackend.PlanQuota{
		Plans:    cloneZcodePlans(quota.Plans),
		Balances: make([]zcodebackend.PlanBalance, len(quota.Balances)),
	}
	for i, balance := range quota.Balances {
		cloned.Balances[i] = balance
		cloned.Balances[i].Capabilities = append([]string(nil), balance.Capabilities...)
	}
	return cloned
}
