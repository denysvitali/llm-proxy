package server

import (
	"context"
	"encoding/base64"
	"fmt"
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

// record writes counters, failure status, and histograms in one transaction.
// Using the attempt timestamp keeps all adapters in the same five-minute bucket.
func (r *redisStats) record(ctx context.Context, a completedAttempt, retentionDays int) error {
	modelName := a.backend + "\x00" + a.model
	bucketPrefix := fmt.Sprintf("bucket:%d:", a.at.Unix()/300)
	pipe := r.client.TxPipeline()
	pipe.SAdd(ctx, r.modelsKey(), modelName)
	for field, value := range a.counters() {
		if value != 0 {
			pipe.HIncrBy(ctx, r.modelKey(modelName), field, value)
			pipe.HIncrBy(ctx, r.modelKey(modelName), bucketPrefix+field, value)
		}
	}
	if a.ttft > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucketPrefix+"ttft:"+strconv.Itoa(histIndex(ttftEdges, a.ttft)), 1)
	}
	if a.e2e > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucketPrefix+"e2e:"+strconv.Itoa(histIndex(e2eEdges, a.e2e)), 1)
	}
	if a.throughput > 0 {
		pipe.HIncrBy(ctx, r.modelKey(modelName), bucketPrefix+"tps:"+strconv.Itoa(histIndex(tpsEdges, a.throughput)), 1)
	}
	return r.commit(ctx, pipe, modelName, retentionDays)
}

func (r *redisStats) commit(ctx context.Context, pipe redis.Pipeliner, modelName string, retentionDays int) error {
	if retentionDays > 0 {
		pipe.Expire(ctx, r.modelKey(modelName), time.Duration(retentionDays+1)*24*time.Hour)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	_, err := r.client.Publish(ctx, r.updatesKey(), "stats-updated").Result()
	return err
}

func (r *redisStats) recordToolErrors(ctx context.Context, backend, model string, n int64, at time.Time, retentionDays int) error {
	if n <= 0 {
		return nil
	}
	modelName := backend + "\x00" + model
	field := fmt.Sprintf("bucket:%d:tool_errors", at.Unix()/300)
	pipe := r.client.TxPipeline()
	pipe.SAdd(ctx, r.modelsKey(), modelName)
	pipe.HIncrBy(ctx, r.modelKey(modelName), "tool_errors", n)
	pipe.HIncrBy(ctx, r.modelKey(modelName), field, n)
	return r.commit(ctx, pipe, modelName, retentionDays)
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
		case "cache_write":
			b.CacheWrite = n
		case "tool_calls":
			b.ToolCalls = n
		case "tool_errors":
			b.ToolErrors = n
		case "status":
			if len(parts) != 4 || n == 0 {
				continue
			}
			if b.StatusCodes == nil {
				b.StatusCodes = map[string]uint64{}
			}
			b.StatusCodes[parts[3]] += n
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

func (model redisModel) snapshot() modelSnapshot {
	snapshot := modelSnapshot{
		Requests: redisUint(model.fields["requests"]), Successes: redisUint(model.fields["successes"]),
		TokensIn: redisUint(model.fields["tokens_in"]), TokensOut: redisUint(model.fields["tokens_out"]),
		CacheRead: redisUint(model.fields["cache_read"]), CacheWrite: redisUint(model.fields["cache_write"]),
		ToolCalls: redisUint(model.fields["tool_calls"]), ToolErrors: redisUint(model.fields["tool_errors"]),
		Statuses: map[string]uint64{}, Buckets: make([]bucket, 0, len(model.buckets)),
	}
	for field, value := range model.fields {
		if status, ok := strings.CutPrefix(field, "status:"); ok {
			if n := redisUint(value); n > 0 {
				snapshot.Statuses[status] = n
			}
		}
	}
	for _, b := range model.buckets {
		snapshot.Buckets = append(snapshot.Buckets, *b)
	}
	return snapshot
}

func redisModelStat(model redisModel, retentionDays int) (ModelStat, bool) {
	return summarizeModel(model.name, model.snapshot(), retentionDays, time.Now())
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

func (st *Stats) seriesAtScopeRedis(ctx context.Context, rng string, now time.Time, backendName, modelName string) (scopedSeriesSet, []string, error) {
	models, err := st.redis.loadModels(ctx)
	if err != nil {
		return scopedSeriesSet{}, nil, err
	}
	snapshots := make(map[string]modelSnapshot, len(models))
	for _, model := range models {
		snapshots[model.name] = model.snapshot()
	}
	return seriesFromModels(snapshots, rng, now, backendName, modelName, st.cfg.RetentionDays)
}
