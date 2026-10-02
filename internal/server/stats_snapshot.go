package server

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// ---------------------------------------------------------------------------
// Aggregation for /stats and the dashboard

// ModelStat is one backend/model row of the stats summary. Latency fields
// are seconds, throughput tokens/second.
type ModelStat struct {
	Backend          string            `json:"backend"`
	Model            string            `json:"model"`
	Requests         uint64            `json:"requests"`
	Successes        uint64            `json:"successes"`
	Uptime           float64           `json:"uptime"` // successful / total requests
	TTFT             Percentiles       `json:"ttft_seconds"`
	E2E              Percentiles       `json:"e2e_seconds"`
	Throughput       Percentiles       `json:"throughput_tps"`
	InputTokens      uint64            `json:"input_tokens"`
	OutputTokens     uint64            `json:"output_tokens"`
	CacheReadTokens  uint64            `json:"cache_read_tokens"`
	CacheWriteTokens uint64            `json:"cache_write_tokens"`
	CacheRate        float64           `json:"cache_rate"` // cached input / total input
	ToolCalls        uint64            `json:"tool_calls"`
	ToolErrors       uint64            `json:"tool_errors"`
	ToolErrorRate    float64           `json:"tool_error_rate"`
	StatusCodes      map[string]uint64 `json:"status_codes,omitempty"` // non-2xx replies by HTTP status
}

// Percentiles carries p50/p90/p99 of one distribution in its source unit.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

// snapshot aggregates the per-model summary. Redis and JSON preserve lifetime
// counters with retained latency windows; otherwise Prometheus supplies this
// process's counters and latency observations.
func (st *Stats) snapshot() []ModelStat {
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if snapshot, err := st.redis.snapshot(ctx, st.cfg.RetentionDays); err == nil {
			return snapshot
		}
	}
	if st.cfg.PersistFile == "" {
		return st.snapshotFromPrometheus()
	}
	return st.snapshotFromBuckets()
}

func (st *Stats) snapshotFromPrometheus() []ModelStat {
	type row struct {
		stat               ModelStat
		ttft, e2e, through *dto.Histogram
		body200Failures    uint64 // 2xx replies whose body was an error object
	}
	rows := map[string]*row{}

	get := func(backendName, model string) *row {
		key := backendName + "\x00" + model
		r, ok := rows[key]
		if !ok {
			r = &row{}
			r.stat.Backend = backendName
			r.stat.Model = model
			rows[key] = r
		}
		return r
	}

	collectInto := func(vec *prometheus.MetricVec, fn func(labels []string, m *dto.Metric)) {
		ch := make(chan prometheus.Metric, 64)
		go func() {
			vec.Collect(ch)
			close(ch)
		}()
		for m := range ch {
			dm := &dto.Metric{}
			if m.Write(dm) != nil || len(dm.Label) < 2 {
				continue
			}
			labels := make([]string, len(dm.Label))
			for i, l := range dm.Label {
				labels[i] = l.GetValue()
			}
			fn(labels, dm)
		}
	}

	collectInto(st.requests.MetricVec, func(labels []string, m *dto.Metric) {
		v := m.GetCounter().GetValue()
		r := get(labels[0], labels[1])
		r.stat.Requests += uint64(v)
		if strings.HasPrefix(labels[2], "2") {
			r.stat.Successes += uint64(v)
		}
	})
	collectInto(st.tokens.MetricVec, func(labels []string, m *dto.Metric) {
		v := uint64(m.GetCounter().GetValue())
		r := get(labels[0], labels[1])
		switch labels[2] {
		case tokenInput:
			r.stat.InputTokens += v
		case tokenOutput:
			r.stat.OutputTokens += v
		case tokenCacheRead:
			r.stat.CacheReadTokens += v
		case tokenCacheWrit:
			r.stat.CacheWriteTokens += v
		}
	})
	collectInto(st.calls.MetricVec, func(labels []string, m *dto.Metric) {
		get(labels[0], labels[1]).stat.ToolCalls += uint64(m.GetCounter().GetValue())
	})
	collectInto(st.errs.MetricVec, func(labels []string, m *dto.Metric) {
		get(labels[0], labels[1]).stat.ToolErrors += uint64(m.GetCounter().GetValue())
	})
	collectInto(st.statuses.MetricVec, func(labels []string, m *dto.Metric) {
		v := uint64(m.GetCounter().GetValue())
		r := get(labels[0], labels[1])
		if strings.HasPrefix(labels[2], "2") {
			// Body-failures (HTTP 200 carrying an error object) were recorded
			// under the same status label as real successes; net them out of
			// the successes count so uptime stays honest.
			r.body200Failures += v
		}
		if r.stat.StatusCodes == nil {
			r.stat.StatusCodes = map[string]uint64{}
		}
		r.stat.StatusCodes[labels[2]] += uint64(m.GetCounter().GetValue())
	})
	hist := func(vec *prometheus.HistogramVec, dst func(*row) **dto.Histogram) {
		collectInto(vec.MetricVec, func(labels []string, m *dto.Metric) {
			if hm := m.GetHistogram(); hm != nil {
				p := dst(get(labels[0], labels[1]))
				*p = hm
			}
		})
	}
	hist(st.ttft, func(r *row) **dto.Histogram { return &r.ttft })
	hist(st.e2e, func(r *row) **dto.Histogram { return &r.e2e })
	hist(st.through, func(r *row) **dto.Histogram { return &r.through })

	out := make([]ModelStat, 0, len(rows))
	for _, r := range rows {
		s := r.stat
		if s.Successes >= r.body200Failures {
			s.Successes -= r.body200Failures
		} else {
			s.Successes = 0
		}
		s.TTFT = percentilesOf(r.ttft)
		s.E2E = percentilesOf(r.e2e)
		s.Throughput = percentilesOf(r.through)
		finishModelStat(&s)
		out = append(out, s)
	}
	sortModelStats(out)
	return out
}

func ratio(num, den uint64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// percentilesOf derives p50/p90/p99 from a Prometheus histogram's cumulative
// buckets; histograms with no samples yield zeros.
func percentilesOf(h *dto.Histogram) Percentiles {
	if h == nil || h.GetSampleCount() == 0 {
		return Percentiles{}
	}
	return Percentiles{
		P50: bucketQuantile(0.50, h),
		P90: bucketQuantile(0.90, h),
		P99: bucketQuantile(0.99, h),
	}
}

// bucketQuantile interpolates q inside the histogram's cumulative buckets,
// mirroring Prometheus's histogram_quantile: upper bound of the bucket where
// the rank falls, linearly interpolated against the previous bound.
func bucketQuantile(q float64, h *dto.Histogram) float64 {
	if h == nil || h.GetSampleCount() == 0 {
		return 0
	}
	buckets := h.GetBucket()
	edges := make([]float64, len(buckets))
	counts := make([]uint64, len(buckets))
	total := h.GetSampleCount()
	for i, b := range buckets {
		edges[i] = b.GetUpperBound()
		counts[i] = b.GetCumulativeCount()
	}
	// If the rank falls beyond the last explicit bucket, the remaining mass
	// sits in the implicit +Inf bucket: fall back to the mean (HEAD behavior).
	if q*float64(total) > float64(counts[len(counts)-1]) {
		return h.GetSampleSum() / float64(total)
	}
	// Prometheus dto buckets are cumulative; histogramQuantile expects
	// per-bucket counts.
	for i := len(counts) - 1; i > 0; i-- {
		counts[i] -= counts[i-1]
	}
	return histogramQuantile(q, edges, counts, h.GetSampleSum())
}

// histogramQuantile computes q from per-bucket counts (NOT cumulative) and
// fixed edges, linearly interpolating inside the rank's bucket the way
// Prometheus's histogram_quantile does. If the rank falls beyond the last
// explicit edge, the remaining mass sits in the implicit +Inf bucket and the
// mean is returned instead of 0 (sum must be supplied for that case).
func histogramQuantile(q float64, edges []float64, counts []uint64, sum float64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	rank := q * float64(total)
	if rank <= 0 {
		return 0
	}
	var prev, prevBound float64
	for i, c := range counts {
		cum := prev + float64(c)
		bound := edges[i]
		if cum >= rank {
			if math.IsInf(bound, 1) {
				return prevBound
			}
			if cum == prev {
				return bound
			}
			share := (rank - prev) / (cum - prev)
			return prevBound + (bound-prevBound)*share
		}
		prev = cum
		prevBound = bound
	}
	// Rank sits in the implicit +Inf bucket; fall back to the mean.
	if total > 0 {
		return sum / float64(total)
	}
	return 0
}

// percentilesFromCounts derives p50/p90/p99 from per-bucket counts.
func percentilesFromCounts(counts []uint64, edges []float64) Percentiles {
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return Percentiles{}
	}
	return Percentiles{
		P50: histogramQuantile(0.50, edges, counts, 0),
		P90: histogramQuantile(0.90, edges, counts, 0),
		P99: histogramQuantile(0.99, edges, counts, 0),
	}
}

func (st *Stats) snapshotFromBuckets() []ModelStat {
	return summarizeModels(st.snapshotModels(), st.cfg.RetentionDays, time.Now())
}

func summarizeModels(models map[string]modelSnapshot, retentionDays int, now time.Time) []ModelStat {
	out := make([]ModelStat, 0, len(models))
	for key, model := range models {
		if stat, ok := summarizeModel(key, model, retentionDays, now); ok {
			out = append(out, stat)
		}
	}
	sortModelStats(out)
	return out
}

// summarizeModel is shared by JSON/memory and Redis. Lifetime counters remain
// lifetime totals; retention applies only to the latency observations.
func summarizeModel(key string, model modelSnapshot, retentionDays int, now time.Time) (ModelStat, bool) {
	backend, name, ok := strings.Cut(key, "\x00")
	if !ok {
		return ModelStat{}, false
	}
	stat := ModelStat{
		Backend: backend, Model: name, Requests: model.Requests, Successes: model.Successes,
		InputTokens: model.TokensIn, OutputTokens: model.TokensOut,
		CacheReadTokens: model.CacheRead, CacheWriteTokens: model.CacheWrite,
		ToolCalls: model.ToolCalls, ToolErrors: model.ToolErrors,
		StatusCodes: nonEmpty(model.Statuses),
	}
	aggregate := newBucketAggregate()
	cutoff := retentionCutoff(now, retentionDays)
	for _, b := range model.Buckets {
		if b.WindowStart.Unix()/300 >= cutoff {
			aggregate.add(b)
		}
	}
	stat.TTFT = percentilesFromCounts(aggregate.TTFTBuckets, ttftEdges)
	stat.E2E = percentilesFromCounts(aggregate.E2EBuckets, e2eEdges)
	stat.Throughput = percentilesFromCounts(aggregate.ThroughputBuckets, tpsEdges)
	finishModelStat(&stat)
	return stat, true
}

func finishModelStat(stat *ModelStat) {
	stat.Uptime = ratio(stat.Successes, stat.Requests)
	stat.CacheRate = ratio(stat.CacheReadTokens, stat.InputTokens)
	stat.ToolErrorRate = ratio(stat.ToolErrors, stat.ToolCalls)
}

func retentionCutoff(now time.Time, days int) int64 {
	if days <= 0 {
		return 0
	}
	return now.Unix()/300 - int64(days)*24*12
}

// bucketAggregate folds retained buckets identically for summaries and series.
type bucketAggregate struct{ bucket }

func newBucketAggregate() bucketAggregate {
	return bucketAggregate{bucket: bucket{
		TTFTBuckets:       make([]uint64, len(ttftEdges)),
		E2EBuckets:        make([]uint64, len(e2eEdges)),
		ThroughputBuckets: make([]uint64, len(tpsEdges)),
	}}
}

func (a *bucketAggregate) add(b bucket) {
	a.Requests += b.Requests
	a.Successes += b.Successes
	a.TokensIn += b.TokensIn
	a.TokensOut += b.TokensOut
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	a.ToolCalls += b.ToolCalls
	a.ToolErrors += b.ToolErrors
	addHistogram(a.TTFTBuckets, b.TTFTBuckets)
	addHistogram(a.E2EBuckets, b.E2EBuckets)
	addHistogram(a.ThroughputBuckets, b.ThroughputBuckets)
}

func addHistogram(dst, src []uint64) {
	for i := 0; i < len(dst) && i < len(src); i++ {
		dst[i] += src[i]
	}
}

// nonEmpty returns the map as-is when it holds entries, nil otherwise, so
// JSON payloads omit rather than show "{}" for healthy models.
func nonEmpty(m map[string]uint64) map[string]uint64 {
	if len(m) == 0 {
		return nil
	}
	return m
}

func sortModelStats(stats []ModelStat) {
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Backend != stats[j].Backend {
			return stats[i].Backend < stats[j].Backend
		}
		return stats[i].Model < stats[j].Model
	})
}
