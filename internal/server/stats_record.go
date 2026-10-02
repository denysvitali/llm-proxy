package server

import (
	"context"
	"time"
)

// completedAttempt is the immutable observation shared by the process-local
// metrics, memory/JSON storage, and Redis adapters. It never retains a body.
type completedAttempt struct {
	at             time.Time
	backend, model string
	status         string
	success        bool
	message        string
	usage          usageReport
	ttft, e2e      float64
	throughput     float64
	request        InspectedRequest
}

func (a completedAttempt) counters() map[string]int64 {
	counters := map[string]int64{
		"requests": 1, "tokens_in": a.usage.input, "tokens_out": a.usage.output,
		"cache_read": a.usage.cacheRead, "cache_write": a.usage.cacheWrite,
		"tool_calls": a.usage.toolCalls,
	}
	if a.success {
		counters["successes"] = 1
	} else {
		counters["status:"+a.status] = 1
	}
	return counters
}

func (st *Stats) recordAttempt(a completedAttempt) {
	st.recordAttemptMetrics(a)
	st.recordAttemptMemory(a)

	a.request.At, a.request.Status, a.request.Error = a.at, a.status, a.message
	st.recordRequest(a.request)
	if !a.success {
		message := a.message
		if message == "" {
			message = "upstream returned status " + a.status
		}
		st.recentMu.Lock()
		st.recent = append(st.recent, UpstreamErrorEvent{
			At: a.at, Backend: a.backend, Model: a.model, Status: a.status,
			Message: message, RequestID: a.request.ID,
		})
		if n := len(st.recent); n > maxRecentErrors {
			st.recent = st.recent[n-maxRecentErrors:]
		}
		st.recentMu.Unlock()
	}
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = st.redis.record(ctx, a, st.cfg.RetentionDays)
		cancel()
	}
	st.updates.notify()
}

func (st *Stats) recordAttemptMetrics(a completedAttempt) {
	st.requests.WithLabelValues(a.backend, a.model, a.status).Inc()
	for label, value := range map[string]int64{
		tokenInput: a.usage.input, tokenOutput: a.usage.output,
		tokenCacheRead: a.usage.cacheRead, tokenCacheWrit: a.usage.cacheWrite,
	} {
		st.tokens.WithLabelValues(a.backend, a.model, label).Add(float64(value))
	}
	if a.usage.toolCalls > 0 {
		st.calls.WithLabelValues(a.backend, a.model).Add(float64(a.usage.toolCalls))
	}
	if !a.success {
		st.statuses.WithLabelValues(a.backend, a.model, a.status).Inc()
	}
	if a.ttft > 0 {
		st.ttft.WithLabelValues(a.backend, a.model).Observe(a.ttft)
	}
	if a.e2e > 0 {
		st.e2e.WithLabelValues(a.backend, a.model).Observe(a.e2e)
	}
	if a.throughput > 0 {
		st.through.WithLabelValues(a.backend, a.model).Observe(a.throughput)
	}
}

func (st *Stats) recordAttemptMemory(a completedAttempt) {
	ms := st.modelFor(a.backend + "\x00" + a.model)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.requests++
	if a.success {
		ms.successes++
	} else {
		ms.statuses[a.status]++
	}
	ms.tokensIn += uint64(a.usage.input)
	ms.tokensOut += uint64(a.usage.output)
	ms.cacheRead += uint64(a.usage.cacheRead)
	ms.cacheWrite += uint64(a.usage.cacheWrite)
	ms.toolCalls += uint64(a.usage.toolCalls)

	b := ms.bucketForLocked(a.at.Unix() / 300)
	b.Requests++
	if a.success {
		b.Successes++
	} else {
		b.StatusCodes[a.status]++
	}
	b.TokensIn += uint64(a.usage.input)
	b.TokensOut += uint64(a.usage.output)
	b.CacheRead += uint64(a.usage.cacheRead)
	b.CacheWrite += uint64(a.usage.cacheWrite)
	b.ToolCalls += uint64(a.usage.toolCalls)
	if a.ttft > 0 {
		b.TTFTBuckets[histIndex(ttftEdges, a.ttft)]++
	}
	if a.e2e > 0 {
		b.E2EBuckets[histIndex(e2eEdges, a.e2e)]++
	}
	if a.throughput > 0 {
		b.ThroughputBuckets[histIndex(tpsEdges, a.throughput)]++
	}
	st.evictLocked(ms, a.at)
}
