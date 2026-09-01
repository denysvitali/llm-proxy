package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Time-series API

// point is one timestamped value in a time series.
type point struct {
	TS    string  `json:"ts"`
	Value float64 `json:"value"`
}

// seriesSet holds the fleet-wide series returned by GET /api/stats.
type seriesSet struct {
	Requests      []point `json:"requests"`
	SuccessRate   []point `json:"success_rate"`
	TTFTP50       []point `json:"ttft_p50"`
	E2EP50        []point `json:"e2e_p50"`
	ThroughputP50 []point `json:"throughput_p50"`
	TokensIn      []point `json:"tokens_in"`
	TokensOut     []point `json:"tokens_out"`
	ToolCalls     []point `json:"tool_calls"`
	ToolErrors    []point `json:"tool_errors"`
}

// scopedSeriesSet carries the same fleet-wide series, restricted to one
// optional backend (and optionally model). An empty Model selects every model
// in the backend.
type scopedSeriesSet struct {
	Series seriesSet `json:"series"`
	Models []string  `json:"models"`
}

var seriesRangeBuckets = map[string]time.Duration{
	"1h":  3 * time.Minute,
	"6h":  15 * time.Minute,
	"24h": time.Hour,
	"7d":  6 * time.Hour,
}

var seriesRangePoints = map[string]int{
	"1h":  20,
	"6h":  24,
	"24h": 24,
	"7d":  28,
}

// seriesAt aggregates the in-memory buckets into fleet-wide series for the
// given range, as of now. The current 5-minute bucket is always included,
// even when it began before the requested range. An unknown range returns an
// error.
func (st *Stats) seriesAt(rng string, now time.Time) (seriesSet, []string, error) {
	series, models, err := st.seriesAtScope(rng, now, "", "")
	if err != nil {
		return seriesSet{}, nil, err
	}
	return series.Series, models, nil
}

// seriesAtScope aggregates the same buckets as seriesAt while filtering rows
// by backend and model. Empty selectors mean "all"; this is the shared core
// for fleet, provider, and model histories.
func (st *Stats) seriesAtScope(rng string, now time.Time, backendName, modelName string) (scopedSeriesSet, []string, error) {
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if series, models, err := st.seriesAtScopeRedis(ctx, rng, now, backendName, modelName); err == nil {
			return series, models, nil
		}
	}
	dur, ok := seriesRangeBuckets[rng]
	if !ok {
		return scopedSeriesSet{}, nil, fmt.Errorf("unknown range %q; supported ranges: 1h, 6h, 24h, 7d", rng)
	}
	n := seriesRangePoints[rng]
	current := now.Truncate(5 * time.Minute)
	end := current.Add(dur)
	start := end.Add(-time.Duration(n) * dur)

	type agg struct {
		requests, successes, tokensIn, tokensOut, cacheRead, toolCalls uint64
		toolErrors                                                     uint64
		ttft, e2e, tps                                                 []uint64
	}
	aggs := make([]*agg, n)
	for i := range aggs {
		aggs[i] = &agg{
			ttft: make([]uint64, len(ttftEdges)),
			e2e:  make([]uint64, len(e2eEdges)),
			tps:  make([]uint64, len(tpsEdges)),
		}
	}
	modelSet := make(map[string]struct{})

	st.mu.RLock()
	defer st.mu.RUnlock()
	for key, ms := range st.models {
		rowBackend, rowModel, ok := strings.Cut(key, "\x00")
		if !ok {
			continue
		}
		if (backendName != "" && rowBackend != backendName) ||
			(modelName != "" && rowModel != modelName) {
			continue
		}
		ms.mu.Lock()
		for win, b := range ms.buckets {
			bStart := time.Unix(win*300, 0).UTC()
			if bStart.Before(start) || !bStart.Before(end) {
				continue
			}
			idx := int(bStart.Sub(start) / dur)
			if idx < 0 || idx >= n {
				continue
			}
			a := aggs[idx]
			a.requests += b.Requests
			a.successes += b.Successes
			a.tokensIn += b.TokensIn
			a.tokensOut += b.TokensOut
			a.cacheRead += b.CacheRead
			a.toolCalls += b.ToolCalls
			a.toolErrors += b.ToolErrors
			for i := range b.TTFTBuckets {
				a.ttft[i] += b.TTFTBuckets[i]
			}
			for i := range b.E2EBuckets {
				a.e2e[i] += b.E2EBuckets[i]
			}
			for i := range b.ThroughputBuckets {
				a.tps[i] += b.ThroughputBuckets[i]
			}
		}
		if len(ms.buckets) > 0 {
			modelSet[rowModel] = struct{}{}
		}
		ms.mu.Unlock()
	}

	scopedModels := make([]string, 0, len(modelSet))
	for model := range modelSet {
		scopedModels = append(scopedModels, model)
	}
	sort.Strings(scopedModels)

	series := seriesSet{
		Requests:      make([]point, 0, n),
		SuccessRate:   make([]point, 0, n),
		TTFTP50:       make([]point, 0, n),
		E2EP50:        make([]point, 0, n),
		ThroughputP50: make([]point, 0, n),
		TokensIn:      make([]point, 0, n),
		TokensOut:     make([]point, 0, n),
		ToolCalls:     make([]point, 0, n),
		ToolErrors:    make([]point, 0, n),
	}
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(i) * dur).Format(time.RFC3339)
		a := aggs[i]
		series.Requests = append(series.Requests, point{TS: ts, Value: float64(a.requests)})
		series.SuccessRate = append(series.SuccessRate, point{TS: ts, Value: ratio(a.successes, a.requests)})
		series.TokensIn = append(series.TokensIn, point{TS: ts, Value: float64(a.tokensIn)})
		series.TokensOut = append(series.TokensOut, point{TS: ts, Value: float64(a.tokensOut)})
		series.ToolCalls = append(series.ToolCalls, point{TS: ts, Value: float64(a.toolCalls)})
		series.ToolErrors = append(series.ToolErrors, point{TS: ts, Value: float64(a.toolErrors)})
		series.TTFTP50 = append(series.TTFTP50, point{TS: ts, Value: histogramQuantile(0.5, ttftEdges, a.ttft, 0)})
		series.E2EP50 = append(series.E2EP50, point{TS: ts, Value: histogramQuantile(0.5, e2eEdges, a.e2e, 0)})
		series.ThroughputP50 = append(series.ThroughputP50, point{TS: ts, Value: histogramQuantile(0.5, tpsEdges, a.tps, 0)})
	}
	return scopedSeriesSet{Series: series, Models: scopedModels}, scopedModels, nil
}
