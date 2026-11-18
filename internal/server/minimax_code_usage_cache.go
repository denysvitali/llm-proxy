package server

import (
	"context"
	"sync"
	"time"

	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
)

const minimaxUsageTTL = time.Minute

// minimaxUsageCache keeps the two independent MiniMax account reads (credits
// and Token Plan, check-in panel) for minimaxUsageTTL. Only successes are
// cached, each half on its own, so one failing service never evicts or blocks
// the other's snapshot. Concurrent readers share one upstream round trip.
// Cached values are never mutated after a fetch; readers only serialize them.
type minimaxUsageCache struct {
	fetchAccount func(context.Context) (*minimaxcodebackend.AccountUsage, error)
	fetchCheckin func(context.Context) (*minimaxcodebackend.CheckinPanel, error)

	mu        sync.Mutex
	account   *minimaxcodebackend.AccountUsage
	accountAt time.Time
	checkin   *minimaxcodebackend.CheckinPanel
	checkinAt time.Time
}

func newMiniMaxUsageCache(manager *minimaxcodebackend.Manager) *minimaxUsageCache {
	cache := &minimaxUsageCache{}
	if manager != nil {
		cache.fetchAccount = manager.Account
		cache.fetchCheckin = manager.CheckinStatus
	}
	return cache
}

// minimaxUsageRead is one combined read; each half carries a value or error.
type minimaxUsageRead struct {
	account    *minimaxcodebackend.AccountUsage
	accountErr error
	checkin    *minimaxcodebackend.CheckinPanel
	checkinErr error
}

// read returns both halves, fetching only expired ones, concurrently.
func (c *minimaxUsageCache) read(ctx context.Context) minimaxUsageRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	out := minimaxUsageRead{account: c.account, checkin: c.checkin}
	refreshAccount := minimaxSnapshotState(c.accountAt, now) != usageSnapshotFresh
	refreshCheckin := minimaxSnapshotState(c.checkinAt, now) != usageSnapshotFresh
	var wg sync.WaitGroup
	if refreshAccount {
		wg.Go(func() { out.account, out.accountErr = c.fetchAccount(ctx) })
	}
	if refreshCheckin {
		wg.Go(func() { out.checkin, out.checkinErr = c.fetchCheckin(ctx) })
	}
	wg.Wait()
	// A failed half reports its error instead of an expired snapshot.
	if out.accountErr != nil {
		out.account = nil
	} else if refreshAccount {
		c.account, c.accountAt = out.account, time.Now()
	}
	if out.checkinErr != nil {
		out.checkin = nil
	} else if refreshCheckin {
		c.checkin, c.checkinAt = out.checkin, time.Now()
	}
	return out
}

// invalidate forgets both snapshots after a claim or sign-in. Taking the lock
// waits for in-flight reads, so a pre-claim fetch cannot land afterwards.
func (c *minimaxUsageCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.account, c.accountAt = nil, time.Time{}
	c.checkin, c.checkinAt = nil, time.Time{}
}

// minimaxSnapshotState treats a zero fetch time as empty (never fetched or
// invalidated).
func minimaxSnapshotState(fetchedAt, now time.Time) usageSnapshotState {
	return usageSnapshotStateAt(!fetchedAt.IsZero(), fetchedAt, minimaxUsageTTL, now)
}
