package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"

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
