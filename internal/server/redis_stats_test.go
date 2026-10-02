package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestRedisStatsAreSharedAcrossInstances(t *testing.T) {
	mini := miniredis.RunT(t)
	redisURL := fmt.Sprintf("redis://%s", mini.Addr())
	cfg := config.StatsConfig{
		RedisURL:       redisURL,
		RedisKeyPrefix: "test:llm-proxy:",
	}
	first := newStats(prometheus.NewRegistry(), cfg)
	second := newStats(prometheus.NewRegistry(), cfg)
	defer func() { _ = first.Close() }()
	defer func() { _ = second.Close() }()

	tracker := first.track("opencode-go", "kimi-k3")
	tracker.rep = usageReport{input: 100, output: 25, cacheRead: 50, toolCalls: 2}
	tracker.setUpstreamStatus(200)
	tracker.noteFirstByte()
	tracker.done()
	second.recordToolErrors("opencode-go", "kimi-k3", 1)

	rows := second.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one shared model row: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Backend != "opencode-go" || row.Model != "kimi-k3" {
		t.Fatalf("row identity = %s/%s", row.Backend, row.Model)
	}
	if row.Requests != 1 || row.Successes != 1 || row.InputTokens != 100 || row.OutputTokens != 25 || row.CacheReadTokens != 50 {
		t.Fatalf("shared counters = %+v", row)
	}
	if row.ToolCalls != 2 || row.ToolErrors != 1 || row.ToolErrorRate != 0.5 {
		t.Fatalf("shared tool stats = %+v", row)
	}

	series, models, err := second.seriesAt("1h", time.Now())
	if err != nil {
		t.Fatalf("seriesAt: %v", err)
	}
	if len(models) != 1 || models[0] != "kimi-k3" {
		t.Fatalf("shared series models = %#v", models)
	}
	var requests float64
	for _, point := range series.Requests {
		requests += point.Value
	}
	if requests != 1 {
		t.Fatalf("shared series requests = %f, want 1", requests)
	}
}

// redisReadHook observes network batches, making the round-trip regression
// deterministic without timing assertions or a remote Redis dependency.
type redisReadHook struct {
	batches         []int
	individualReads int
	failBatch       int
}

func (h *redisReadHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *redisReadHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "hgetall" {
			h.individualReads++
		}
		return next(ctx, cmd)
	}
}

func (h *redisReadHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		reads := 0
		for _, cmd := range cmds {
			if cmd.Name() == "hgetall" {
				reads++
			}
		}
		if reads > 0 {
			h.batches = append(h.batches, reads)
			if len(h.batches) == h.failBatch {
				return errors.New("snapshot batch unavailable")
			}
		}
		return next(ctx, cmds)
	}
}

func TestRedisStatsReadsModelsInBoundedBatches(t *testing.T) {
	mini := miniredis.RunT(t)
	store, err := newRedisStats("redis://"+mini.Addr(), "test:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.close)
	hook := &redisReadHook{}
	store.client.AddHook(hook)

	const modelCount = 260
	for i := range modelCount {
		name := fmt.Sprintf("backend\x00model-%03d", i)
		if _, err := mini.SAdd(store.modelsKey(), name); err != nil {
			t.Fatal(err)
		}
		mini.HSet(store.modelKey(name), "requests", "2", "successes", "1", "tokens_out", "30", "status:503", "1")
	}
	// Expiry leaves an index entry behind; it must not create a zero-valued row.
	if _, err := mini.SAdd(store.modelsKey(), "expired\x00model"); err != nil {
		t.Fatal(err)
	}
	rows, err := store.snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != modelCount {
		t.Fatalf("rows = %d, want %d", len(rows), modelCount)
	}
	for i, row := range rows {
		if row.Backend != "backend" || row.Model != fmt.Sprintf("model-%03d", i) || row.Requests != 2 ||
			row.Successes != 1 || row.Uptime != 0.5 || row.OutputTokens != 30 || row.StatusCodes["503"] != 1 {
			t.Fatalf("row %d = %+v", i, row)
		}
	}
	if hook.individualReads != 0 || len(hook.batches) != 3 {
		t.Fatalf("reads were not batched: individual=%d batches=%v", hook.individualReads, hook.batches)
	}
	for _, size := range hook.batches {
		if size > 128 {
			t.Fatalf("unbounded batch size: %d", size)
		}
	}

	// A later transport failure must not return the earlier batch as a
	// successful, incomplete view of the fleet.
	hook.batches = nil
	hook.failBatch = 2
	models, err := store.loadModels(context.Background())
	if err == nil || models != nil {
		t.Fatalf("failed batch returned partial snapshot: models=%d err=%v", len(models), err)
	}
}

func TestRedisStatsCommandFailureFallsBackToLocal(t *testing.T) {
	mini := miniredis.RunT(t)
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{RedisURL: "redis://" + mini.Addr()})
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now()
	st.recordAttempt(completedAttempt{at: now, backend: "a", model: "m", status: "200", success: true,
		usage: usageReport{input: 7, output: 3}})
	if _, err := mini.SAdd(st.redis.modelsKey(), "bad\x00model"); err != nil {
		t.Fatal(err)
	}
	if err := mini.Set(st.redis.modelKey("bad\x00model"), "not a hash"); err != nil {
		t.Fatal(err)
	}
	if models, err := st.redis.loadModels(context.Background()); err == nil || models != nil {
		t.Fatalf("command failure returned partial snapshot: models=%d err=%v", len(models), err)
	}
	rows := st.snapshot()
	if len(rows) != 1 || rows[0].Backend != "a" || rows[0].Requests != 1 || rows[0].InputTokens != 7 {
		t.Fatalf("local fallback = %+v", rows)
	}
	series, models, err := st.seriesAt("1h", now)
	if err != nil || len(models) != 1 || models[0] != "m" {
		t.Fatalf("local series models=%v err=%v", models, err)
	}
	var requests float64
	for _, point := range series.Requests {
		requests += point.Value
	}
	if requests != 1 {
		t.Fatalf("local series requests=%v, want 1", requests)
	}
}
