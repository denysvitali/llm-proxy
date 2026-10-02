package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
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

// Limit queued replies per round trip while avoiding one network round trip
// per model. A failed batch discards the complete read, so callers fall back
// to local stats rather than displaying a partial fleet snapshot.
const redisStatsReadBatchSize = 128

func (r *redisStats) loadModels(ctx context.Context) (map[string]modelSnapshot, error) {
	names, err := r.client.SMembers(ctx, r.modelsKey()).Result()
	if err != nil {
		return nil, err
	}
	models := make(map[string]modelSnapshot, len(names))
	for start := 0; start < len(names); start += redisStatsReadBatchSize {
		batch := names[start:min(start+redisStatsReadBatchSize, len(names))]
		pipe := r.client.Pipeline()
		commands := make([]*redis.MapStringStringCmd, len(batch))
		for i, name := range batch {
			commands[i] = pipe.HGetAll(ctx, r.modelKey(name))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
		for i, command := range commands {
			fields := command.Val()
			// The model index can outlive an expired model hash.
			if len(fields) > 0 {
				models[batch[i]] = decodeRedisModel(fields)
			}
		}
	}
	return models, nil
}

func (r *redisStats) snapshot(ctx context.Context, retentionDays int) ([]ModelStat, error) {
	models, err := r.loadModels(ctx)
	if err != nil {
		return nil, err
	}
	return summarizeModels(models, retentionDays, time.Now()), nil
}

func (st *Stats) seriesAtScopeRedis(ctx context.Context, rng string, now time.Time, backendName, modelName string) (scopedSeriesSet, []string, error) {
	models, err := st.redis.loadModels(ctx)
	if err != nil {
		return scopedSeriesSet{}, nil, err
	}
	return seriesFromModels(models, rng, now, backendName, modelName, st.cfg.RetentionDays)
}
