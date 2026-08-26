package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStats stores the dashboard's aggregate counters and time buckets in a
// shared Redis instance. Prometheus metrics remain process-local because the
// ServiceMonitor already scrapes every proxy pod independently.
type redisStats struct {
	client *redis.Client
	prefix string
	pubsub *redis.PubSub
	stop   chan struct{}
	once   sync.Once
}

func newRedisStats(url, prefix string) (*redisStats, error) {
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse stats.redis_url: %w", err)
	}
	if prefix == "" {
		prefix = "llm-proxy:stats:"
	}
	return &redisStats{
		client: redis.NewClient(options),
		prefix: prefix,
		stop:   make(chan struct{}),
	}, nil
}

func (r *redisStats) modelsKey() string  { return r.prefix + "models" }
func (r *redisStats) updatesKey() string { return r.prefix + "updates" }

func (r *redisStats) modelKey(model string) string {
	return r.prefix + "model:" + base64.RawURLEncoding.EncodeToString([]byte(model))
}

func (r *redisStats) startUpdates(notify func()) {
	r.pubsub = r.client.Subscribe(context.Background(), r.updatesKey())
	events := r.pubsub.Channel()
	go func() {
		for {
			select {
			case <-r.stop:
				return
			case _, ok := <-events:
				if !ok {
					return
				}
				notify()
			}
		}
	}()
}

func (r *redisStats) close() {
	r.once.Do(func() {
		close(r.stop)
		if r.pubsub != nil {
			_ = r.pubsub.Close()
		}
		_ = r.client.Close()
	})
}

// record atomically adds one completed upstream attempt and its latency
// observations to the shared model hash.
func (r *redisStats) record(ctx context.Context, backend, model, status string, ttft, e2e, throughput float64, rep usageReport, retentionDays int) error {
	modelName := backend + "\x00" + model
	win := time.Now().Unix() / 300
	bucket := fmt.Sprintf("bucket:%d:", win)
	pipe := r.client.TxPipeline()
	pipe.SAdd(ctx, r.modelsKey(), modelName)
	for field, value := range map[string]int64{
		"requests":    1,
		"tokens_in":   rep.input,
		"tokens_out":  rep.output,
		"cache_read":  rep.cacheRead,
		"cache_write": rep.cacheWrite,
		"tool_calls":  rep.toolCalls,
	} {
		if value != 0 || field == "requests" {
			pipe.HIncrBy(ctx, r.modelKey(modelName), field, value)
		}
	}
	if strings.HasPrefix(status, "2") {
		pipe.HIncrBy(ctx, r.modelKey(modelName), "successes", 1)
	}
	pipe.HIncrBy(ctx, r.modelKey(modelName), bucket+"requests", 1)
	if strings.HasPrefix(status, "2") {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucket+"successes", 1)
	}
	for field, value := range map[string]int64{
		bucket + "tokens_in":  rep.input,
		bucket + "tokens_out": rep.output,
		bucket + "cache_read": rep.cacheRead,
		bucket + "tool_calls": rep.toolCalls,
	} {
		if value != 0 {
			pipe.HIncrBy(ctx, r.modelKey(modelName), field, value)
		}
	}
	if ttft > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucket+"ttft:"+strconv.Itoa(histIndex(ttftEdges, ttft)), 1)
	}
	if e2e > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucket+"e2e:"+strconv.Itoa(histIndex(e2eEdges, e2e)), 1)
	}
	if throughput > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucket+"tps:"+strconv.Itoa(histIndex(tpsEdges, throughput)), 1)
	}
	if retentionDays > 0 {
		pipe.Expire(ctx, r.modelKey(modelName), time.Duration(retentionDays+1)*24*time.Hour)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	_, err := r.client.Publish(ctx, r.updatesKey(), "stats-updated").Result()
	return err
}

func (r *redisStats) recordToolErrors(ctx context.Context, backend, model string, n int64, retentionDays int) error {
	if n <= 0 {
		return nil
	}
	modelName := backend + "\x00" + model
	field := fmt.Sprintf("bucket:%d:tool_errors", time.Now().Unix()/300)
	pipe := r.client.TxPipeline()
	pipe.SAdd(ctx, r.modelsKey(), modelName)
	pipe.HIncrBy(ctx, r.modelKey(modelName), "tool_errors", n)
	pipe.HIncrBy(ctx, r.modelKey(modelName), field, n)
	if retentionDays > 0 {
		pipe.Expire(ctx, r.modelKey(modelName), time.Duration(retentionDays+1)*24*time.Hour)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	_, err := r.client.Publish(ctx, r.updatesKey(), "stats-updated").Result()
	return err
}

type redisModel struct {
	name    string
	fields  map[string]string
	buckets map[int64]*bucket
}

func (r *redisStats) loadModels(ctx context.Context) ([]redisModel, error) {
	names, err := r.client.SMembers(ctx, r.modelsKey()).Result()
	if err != nil {
		return nil, err
	}
	models := make([]redisModel, 0, len(names))
	for _, name := range names {
		fields, err := r.client.HGetAll(ctx, r.modelKey(name)).Result()
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			continue
		}
		models = append(models, redisModel{name: name, fields: fields, buckets: redisBuckets(fields)})
	}
	return models, nil
}

func redisBuckets(fields map[string]string) map[int64]*bucket {
	buckets := make(map[int64]*bucket)
	for field, value := range fields {
		parts := strings.Split(field, ":")
		if len(parts) < 3 || parts[0] != "bucket" {
			continue
		}
		win, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		b, ok := buckets[win]
		if !ok {
			b = &bucket{
				WindowStart:       time.Unix(win*300, 0).UTC(),
				TTFTBuckets:       make([]uint64, len(ttftEdges)),
				E2EBuckets:        make([]uint64, len(e2eEdges)),
				ThroughputBuckets: make([]uint64, len(tpsEdges)),
			}
			buckets[win] = b
		}
		n := redisUint(value)
		switch parts[2] {
		case "requests":
			b.Requests = n
		case "successes":
			b.Successes = n
		case "tokens_in":
			b.TokensIn = n
		case "tokens_out":
			b.TokensOut = n
		case "cache_read":
			b.CacheRead = n
		case "tool_calls":
			b.ToolCalls = n
		case "tool_errors":
			b.ToolErrors = n
		case "ttft", "e2e", "tps":
			if len(parts) != 4 {
				continue
			}
			idx, err := strconv.Atoi(parts[3])
			if err != nil {
				continue
			}
			switch parts[2] {
			case "ttft":
				if idx >= 0 && idx < len(b.TTFTBuckets) {
					b.TTFTBuckets[idx] = n
				}
			case "e2e":
				if idx >= 0 && idx < len(b.E2EBuckets) {
					b.E2EBuckets[idx] = n
				}
			case "tps":
				if idx >= 0 && idx < len(b.ThroughputBuckets) {
					b.ThroughputBuckets[idx] = n
				}
			}
		}
	}
	return buckets
}

func redisUint(value string) uint64 {
	n, _ := strconv.ParseUint(value, 10, 64)
	return n
}

func redisModelStat(model redisModel, retentionDays int) (ModelStat, bool) {
	backend, name, ok := strings.Cut(model.name, "\x00")
	if !ok {
		return ModelStat{}, false
	}
	stat := ModelStat{
		Backend:          backend,
		Model:            name,
		Requests:         redisUint(model.fields["requests"]),
		Successes:        redisUint(model.fields["successes"]),
		InputTokens:      redisUint(model.fields["tokens_in"]),
		OutputTokens:     redisUint(model.fields["tokens_out"]),
		CacheReadTokens:  redisUint(model.fields["cache_read"]),
		CacheWriteTokens: redisUint(model.fields["cache_write"]),
		ToolCalls:        redisUint(model.fields["tool_calls"]),
		ToolErrors:       redisUint(model.fields["tool_errors"]),
	}
	ttft := make([]uint64, len(ttftEdges))
	e2e := make([]uint64, len(e2eEdges))
	tps := make([]uint64, len(tpsEdges))
	cutoff := int64(0)
	if retentionDays > 0 {
		cutoff = time.Now().Unix()/300 - int64(retentionDays)*24*12
	}
	for win, b := range model.buckets {
		if cutoff > 0 && win < cutoff {
			continue
		}
		for i := range ttft {
			ttft[i] += b.TTFTBuckets[i]
			e2e[i] += b.E2EBuckets[i]
		}
		for i := range tps {
			tps[i] += b.ThroughputBuckets[i]
		}
	}
	stat.Uptime = ratio(stat.Successes, stat.Requests)
	stat.TTFT = percentilesFromCounts(ttft, ttftEdges)
	stat.E2E = percentilesFromCounts(e2e, e2eEdges)
	stat.Throughput = percentilesFromCounts(tps, tpsEdges)
	stat.CacheRate = ratio(stat.CacheReadTokens, stat.InputTokens)
	stat.ToolErrorRate = ratio(stat.ToolErrors, stat.ToolCalls)
	return stat, true
}

func (r *redisStats) snapshot(ctx context.Context, retentionDays int) ([]ModelStat, error) {
	models, err := r.loadModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModelStat, 0, len(models))
	for _, model := range models {
		if stat, ok := redisModelStat(model, retentionDays); ok {
			out = append(out, stat)
		}
	}
	sortModelStats(out)
	return out, nil
}

func sortModelStats(stats []ModelStat) {
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Backend != stats[j].Backend {
			return stats[i].Backend < stats[j].Backend
		}
		return stats[i].Model < stats[j].Model
	})
}

func (st *Stats) seriesAtScopeRedis(ctx context.Context, rng string, now time.Time, backendName, modelName string) (scopedSeriesSet, []string, error) {
	dur, ok := seriesRangeBuckets[rng]
	if !ok {
		return scopedSeriesSet{}, nil, fmt.Errorf("unknown range %q; supported ranges: 1h, 6h, 24h, 7d", rng)
	}
	n := seriesRangePoints[rng]
	current := now.Truncate(5 * time.Minute)
	end := current.Add(dur)
	start := end.Add(-time.Duration(n) * dur)

	type aggregate struct {
		requests, successes, tokensIn, tokensOut, toolCalls uint64
		ttft, e2e, tps                                      []uint64
	}
	aggs := make([]*aggregate, n)
	for i := range aggs {
		aggs[i] = &aggregate{
			ttft: make([]uint64, len(ttftEdges)),
			e2e:  make([]uint64, len(e2eEdges)),
			tps:  make([]uint64, len(tpsEdges)),
		}
	}

	models, err := st.redis.loadModels(ctx)
	if err != nil {
		return scopedSeriesSet{}, nil, err
	}
	modelSet := make(map[string]struct{})
	cutoff := int64(0)
	if st.cfg.RetentionDays > 0 {
		cutoff = now.Unix()/300 - int64(st.cfg.RetentionDays)*24*12
	}
	for _, model := range models {
		rowBackend, rowModel, ok := strings.Cut(model.name, "\x00")
		if !ok || (backendName != "" && rowBackend != backendName) || (modelName != "" && rowModel != modelName) {
			continue
		}
		for win, b := range model.buckets {
			if cutoff > 0 && win < cutoff {
				continue
			}
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
			a.toolCalls += b.ToolCalls
			for i := range b.TTFTBuckets {
				a.ttft[i] += b.TTFTBuckets[i]
				a.e2e[i] += b.E2EBuckets[i]
			}
			for i := range b.ThroughputBuckets {
				a.tps[i] += b.ThroughputBuckets[i]
			}
		}
		if len(model.buckets) > 0 {
			modelSet[rowModel] = struct{}{}
		}
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
	}
	for i := range aggs {
		ts := start.Add(time.Duration(i) * dur).Format(time.RFC3339)
		a := aggs[i]
		series.Requests = append(series.Requests, point{TS: ts, Value: float64(a.requests)})
		series.SuccessRate = append(series.SuccessRate, point{TS: ts, Value: ratio(a.successes, a.requests)})
		series.TokensIn = append(series.TokensIn, point{TS: ts, Value: float64(a.tokensIn)})
		series.TokensOut = append(series.TokensOut, point{TS: ts, Value: float64(a.tokensOut)})
		series.ToolCalls = append(series.ToolCalls, point{TS: ts, Value: float64(a.toolCalls)})
		series.TTFTP50 = append(series.TTFTP50, point{TS: ts, Value: histogramQuantile(0.5, ttftEdges, a.ttft, 0)})
		series.E2EP50 = append(series.E2EP50, point{TS: ts, Value: histogramQuantile(0.5, e2eEdges, a.e2e, 0)})
		series.ThroughputP50 = append(series.ThroughputP50, point{TS: ts, Value: histogramQuantile(0.5, tpsEdges, a.tps, 0)})
	}
	return scopedSeriesSet{Series: series, Models: scopedModels}, scopedModels, nil
}
