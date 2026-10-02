package server

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

func TestGrokUsageCacheConcurrencyAndCopies(t *testing.T) {
	var calls atomic.Int32
	cache := &grokUsageCache{fetch: func(context.Context) (grokbackend.Usage, error) {
		calls.Add(1)
		return grokbackend.Usage{Billing: grokbackend.Billing{
			MonthlyLimit: grokbackend.Number{Valid: true, Value: 100},
			Used:         grokbackend.Number{Valid: true}, OnDemandUsed: grokbackend.Number{Valid: true},
			OnDemandCap: grokbackend.Number{Valid: true}, PrepaidBalance: grokbackend.Number{Valid: true},
		}}, nil
	}}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			view, err := cache.get(context.Background(), false)
			if err != nil {
				t.Error(err)
				return
			}
			for i, amount := range []*float64{view.LimitCents, view.UsedCents, view.RemainingCents,
				view.OnDemandUsedCents, view.OnDemandCapCents, view.PrepaidCents} {
				want := float64(0)
				if i == 0 || i == 2 {
					want = 100
				}
				if amount == nil || *amount != want {
					t.Errorf("amount %d = %v, want %v", i, amount, want)
					continue
				}
				*amount = 999 // A caller must not mutate another caller's view.
			}
		})
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent reads fetched %d times, want 1", got)
	}
	unknown := cloneGrokUsage(grokbackend.UsageView{})
	if unknown.LimitCents != nil || unknown.UsedCents != nil || unknown.RemainingCents != nil ||
		unknown.OnDemandUsedCents != nil || unknown.OnDemandCapCents != nil || unknown.PrepaidCents != nil {
		t.Fatal("unknown amounts became known zero amounts")
	}
}

func TestGrokUsageCacheRefreshAndFailure(t *testing.T) {
	calls := 0
	fetchErr := errors.New("usage unavailable")
	fail := false
	cache := &grokUsageCache{fetch: func(context.Context) (grokbackend.Usage, error) {
		calls++
		if fail {
			return grokbackend.Usage{}, fetchErr
		}
		return grokbackend.Usage{}, nil
	}}
	ctx := context.Background()
	first, err := cache.get(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := cache.get(ctx, true); !errors.Is(err, fetchErr) {
		t.Fatalf("forced refresh error = %v", err)
	}
	if got, err := cache.get(ctx, false); err != nil || got.FetchedAt != first.FetchedAt || calls != 2 {
		t.Fatalf("failed forced refresh lost fresh cache: view=%+v calls=%d err=%v", got, calls, err)
	}
	cache.value.FetchedAt = time.Now().Add(-grokUsageTTL)
	if _, err := cache.get(ctx, false); !errors.Is(err, fetchErr) {
		t.Fatalf("expired cache returned stale success: %v", err)
	}
	fail = false
	if _, err := cache.get(ctx, false); err != nil || calls != 4 {
		t.Fatalf("failed fetch was cached: calls=%d err=%v", calls, err)
	}
}

func TestZcodeUsageCacheCopiesAndShapes(t *testing.T) {
	plans := []zcodebackend.PlanUsage{{PlanID: "plan", Entitlements: []zcodebackend.PlanEntitlement{{ShowName: "model"}}}}
	quota := zcodebackend.PlanQuota{Plans: plans, Balances: []zcodebackend.PlanBalance{{Capabilities: []string{"chat"}}}}
	cache := &zcodeUsageCache{
		fetchPlans: func(context.Context) ([]zcodebackend.PlanUsage, error) { return plans, nil },
		fetchQuota: func(context.Context) (zcodebackend.PlanQuota, error) { return quota, nil },
	}
	ctx := context.Background()
	readPlans, err := cache.plans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readQuota, err := cache.quota(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readPlans[0].Entitlements[0].ShowName = "caller mutation"
	readQuota.Plans[0].Entitlements[0].ShowName = "caller mutation"
	readQuota.Balances[0].Capabilities[0] = "caller mutation"
	plans[0].Entitlements[0].ShowName = "source mutation"
	quota.Balances[0].Capabilities[0] = "source mutation"
	readPlans, err = cache.plans(ctx)
	if err != nil || readPlans[0].Entitlements[0].ShowName != "model" {
		t.Fatalf("plans share nested data: %+v err=%v", readPlans, err)
	}
	readQuota, err = cache.quota(ctx)
	if err != nil || readQuota.Plans[0].Entitlements[0].ShowName != "model" || readQuota.Balances[0].Capabilities[0] != "chat" {
		t.Fatalf("quota shares nested data: %+v err=%v", readQuota, err)
	}
	for _, input := range []zcodebackend.PlanQuota{{}, {Plans: []zcodebackend.PlanUsage{}, Balances: []zcodebackend.PlanBalance{}}} {
		data, err := json.Marshal(cloneZcodeQuota(input))
		if err != nil || string(data) != `{"plans":null,"balances":[]}` {
			t.Fatalf("empty quota shape = %s err=%v", data, err)
		}
	}
}

func TestZcodeUsageCacheConcurrencyAndFailures(t *testing.T) {
	var planCalls, quotaCalls atomic.Int32
	var fail atomic.Bool
	fetchErr := errors.New("billing unavailable")
	cache := &zcodeUsageCache{
		fetchPlans: func(context.Context) ([]zcodebackend.PlanUsage, error) {
			planCalls.Add(1)
			if fail.Load() {
				return nil, fetchErr
			}
			return nil, nil
		},
		fetchQuota: func(context.Context) (zcodebackend.PlanQuota, error) {
			quotaCalls.Add(1)
			if fail.Load() {
				return zcodebackend.PlanQuota{}, fetchErr
			}
			return zcodebackend.PlanQuota{}, nil
		},
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := cache.plans(ctx); err != nil {
				t.Error(err)
			}
			if _, err := cache.quota(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if planCalls.Load() != 1 || quotaCalls.Load() != 1 {
		t.Fatalf("concurrent empty successes not cached: plans=%d quota=%d", planCalls.Load(), quotaCalls.Load())
	}
	cache.plansAt = time.Now().Add(-zcodeUsageTTL)
	cache.quotaAt = time.Now().Add(-zcodeUsageTTL)
	fail.Store(true)
	if _, err := cache.plans(ctx); !errors.Is(err, fetchErr) {
		t.Fatalf("expired plans returned stale success: %v", err)
	}
	if _, err := cache.quota(ctx); !errors.Is(err, fetchErr) {
		t.Fatalf("expired quota returned stale success: %v", err)
	}
	fail.Store(false)
	if _, err := cache.plans(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.quota(ctx); err != nil {
		t.Fatal(err)
	}
	if planCalls.Load() != 3 || quotaCalls.Load() != 3 {
		t.Fatalf("failures were cached: plans=%d quota=%d", planCalls.Load(), quotaCalls.Load())
	}
}

func TestZcodeUsageCacheInvalidationWaitsForInflightReads(t *testing.T) {
	var planCalls, quotaCalls atomic.Int32
	quotaStarted := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRead)
	cache := &zcodeUsageCache{
		fetchPlans: func(context.Context) ([]zcodebackend.PlanUsage, error) {
			planCalls.Add(1)
			return nil, nil
		},
		fetchQuota: func(context.Context) (zcodebackend.PlanQuota, error) {
			if quotaCalls.Add(1) == 1 {
				close(quotaStarted)
				<-release
			}
			return zcodebackend.PlanQuota{}, nil
		},
	}
	ctx := context.Background()
	if _, err := cache.plans(ctx); err != nil {
		t.Fatal(err)
	}
	var read sync.WaitGroup
	read.Go(func() {
		if _, err := cache.quota(ctx); err != nil {
			t.Error(err)
		}
	})
	<-quotaStarted
	invalidated := make(chan struct{})
	go func() { cache.invalidate(); close(invalidated) }()
	// The plans read has finished, so only invalidate can own plansMu.
	// Observing it locked establishes that invalidate has started and must
	// wait for the in-flight quota fetch before clearing either timestamp.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for cache.plansMu.TryLock() {
		cache.plansMu.Unlock()
		select {
		case <-invalidated:
			t.Fatal("invalidation returned while a pre-claim read was active")
		case <-deadline.C:
			t.Fatal("invalidation did not acquire the plans lock")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-invalidated:
		t.Fatal("invalidation returned while a pre-claim read was active")
	default:
	}
	releaseRead()
	read.Wait()
	<-invalidated
	if _, err := cache.plans(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.quota(ctx); err != nil {
		t.Fatal(err)
	}
	if planCalls.Load() != 2 || quotaCalls.Load() != 2 {
		t.Fatalf("pre-claim reads repopulated cache: plans=%d quota=%d", planCalls.Load(), quotaCalls.Load())
	}
}
