package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestStatsStorageParity(t *testing.T) {
	mini := miniredis.RunT(t)
	local := newStats(prometheus.NewRegistry(), config.StatsConfig{
		PersistFile: filepath.Join(t.TempDir(), "stats.json"), RetentionDays: 1,
	})
	shared := newStats(prometheus.NewRegistry(), config.StatsConfig{
		RedisURL: "redis://" + mini.Addr(), RetentionDays: 1,
	})
	t.Cleanup(func() { _ = local.Close(); _ = shared.Close() })
	now := time.Now().UTC()
	attempts := []completedAttempt{
		{at: now.Add(-48 * time.Hour), backend: "a", model: "m", status: "503", ttft: 50, e2e: 55},
		{at: now, backend: "a", model: "m", status: "200", success: true, ttft: 0.1, e2e: 1, throughput: 30,
			usage: usageReport{input: 100, output: 30, cacheRead: 20, cacheWrite: 7, toolCalls: 2}},
		{at: now.Add(-10 * time.Minute), backend: "a", model: "m", status: "429", ttft: 0.2, e2e: 0.5},
		{at: now, backend: "a", model: "m", status: statusError, message: "connection failed"},
		{at: now, backend: "a", model: "m", status: "200", message: "error in success body"},
		{at: now, backend: "b", model: "other", status: "201", success: true,
			usage: usageReport{input: 20, output: 5, cacheWrite: 3}},
	}
	for _, attempt := range attempts {
		local.recordAttempt(attempt)
		shared.recordAttempt(attempt)
	}
	local.recordToolErrors("a", "m", 1)
	shared.recordToolErrors("a", "m", 1)

	want := local.snapshot()
	if len(want) != 2 || want[0].Requests != 5 || want[0].Successes != 1 || want[0].CacheWriteTokens != 7 || want[0].ToolErrors != 1 {
		t.Fatalf("local totals = %+v", want)
	}
	if expected := map[string]uint64{"503": 1, "429": 1, "error": 1, "200": 1}; !reflect.DeepEqual(want[0].StatusCodes, expected) {
		t.Fatalf("failure statuses = %v, want %v", want[0].StatusCodes, expected)
	}
	if got := shared.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Redis summary = %+v, memory summary = %+v", got, want)
	}
	redisModels, err := shared.redis.loadModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, model := range redisModels {
		localModel := local.snapshotForPersist().Models[name]
		redisBuckets := make(map[time.Time]bucket, len(model.Buckets))
		for _, b := range model.Buckets {
			redisBuckets[b.WindowStart] = b
		}
		for _, wantBucket := range localModel.Buckets {
			gotBucket := redisBuckets[wantBucket.WindowStart]
			wantJSON, err := json.Marshal(wantBucket)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(gotBucket)
			if err != nil {
				t.Fatal(err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("bucket %s differs: Redis=%s memory=%s", wantBucket.WindowStart, gotJSON, wantJSON)
			}
		}
	}
	// Prometheus keeps its established histogram boundaries and process-local
	// observations. The counters and derived rates must agree across adapters.
	if got := withoutPercentiles(shared.snapshotFromPrometheus()); !reflect.DeepEqual(got, withoutPercentiles(want)) {
		t.Fatalf("Prometheus counters = %+v, storage counters = %+v", got, withoutPercentiles(want))
	}
	for _, scope := range [][2]string{{"", ""}, {"a", ""}, {"a", "m"}, {"b", "other"}, {"missing", ""}} {
		for _, rng := range []string{"1h", "6h", "24h", "7d"} {
			wantSeries, _, err := local.seriesAtScope(rng, now, scope[0], scope[1])
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := shared.seriesAtScope(rng, now, scope[0], scope[1])
			if err != nil || !reflect.DeepEqual(got, wantSeries) {
				t.Fatalf("%s scope %v differs: Redis=%+v memory=%+v err=%v", rng, scope, got, wantSeries, err)
			}
		}
	}

	local.persist()
	reloaded := newStats(prometheus.NewRegistry(), local.cfg)
	t.Cleanup(func() { _ = reloaded.Close() })
	if err := reloaded.load(local.cfg.PersistFile); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON round trip = %+v, want %+v", got, want)
	}
	if got := reloaded.snapshotFromPrometheus(); len(got) != 0 {
		t.Fatalf("restored history leaked into process-local metrics: %+v", got)
	}
	saved := reloaded.snapshotForPersist().Models["a\x00m"]
	if len(saved.Buckets) != 2 || saved.Buckets[1].CacheWrite != 7 || saved.Statuses["503"] != 1 {
		t.Fatalf("retention or persisted cache-write/status totals lost: %+v", saved)
	}

	// An unavailable Redis still falls back to this instance's metrics/series.
	if err := shared.redis.client.Close(); err != nil {
		t.Fatal(err)
	}
	if got := withoutPercentiles(shared.snapshot()); !reflect.DeepEqual(got, withoutPercentiles(want)) {
		t.Fatalf("Redis fallback counters = %+v, want %+v", got, withoutPercentiles(want))
	}
	wantSeries, _, _ := local.seriesAt("1h", now)
	if got, _, err := shared.seriesAt("1h", now); err != nil || !reflect.DeepEqual(got, wantSeries) {
		t.Fatalf("Redis fallback series differs: err=%v", err)
	}
}

func withoutPercentiles(rows []ModelStat) []ModelStat {
	out := append([]ModelStat(nil), rows...)
	for i := range out {
		out[i].TTFT, out[i].E2E, out[i].Throughput = Percentiles{}, Percentiles{}, Percentiles{}
	}
	return out
}

func TestStatsSnapshotIsDetached(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	a := completedAttempt{at: time.Now(), backend: "a", model: "m", status: "503", ttft: 0.2, e2e: 0.4, throughput: 5,
		usage: usageReport{input: 10, output: 2, cacheWrite: 3}}
	st.recordAttempt(a)
	snapshot := st.snapshotForPersist()
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	st.recordAttempt(a)
	after, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("recording another attempt mutated a detached snapshot")
	}
	model := snapshot.Models["a\x00m"]
	model.Buckets[0].StatusCodes["503"] = 100
	model.Buckets[0].TTFTBuckets[histIndex(ttftEdges, a.ttft)] = 100
	model.Buckets[0].E2EBuckets[histIndex(e2eEdges, a.e2e)] = 100
	model.Buckets[0].ThroughputBuckets[histIndex(tpsEdges, a.throughput)] = 100
	model.Statuses["503"] = 100
	fresh := st.snapshotForPersist().Models["a\x00m"]
	if fresh.Statuses["503"] != 2 || fresh.Buckets[0].StatusCodes["503"] != 2 ||
		fresh.Buckets[0].TTFTBuckets[histIndex(ttftEdges, a.ttft)] != 2 ||
		fresh.Buckets[0].E2EBuckets[histIndex(e2eEdges, a.e2e)] != 2 ||
		fresh.Buckets[0].ThroughputBuckets[histIndex(tpsEdges, a.throughput)] != 2 {
		t.Fatal("snapshot map/histogram mutation changed live stats")
	}
}

func TestStatsLegacyPersistenceRetainsCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	// Original v1 files may omit cache_write and model-level status_codes.
	legacy := fmt.Sprintf(`{"version":1,"models":{"a\u0000m":{"requests":1,"tokens_in":10,"buckets":[{"window_start":%q,"requests":1,"status_codes":{"503":1}}]}}}`, old)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{PersistFile: path, RetentionDays: 1})
	if err := st.load(path); err != nil {
		t.Fatal(err)
	}
	st.recordAttempt(completedAttempt{at: time.Now(), backend: "a", model: "m", status: "200", success: true,
		usage: usageReport{cacheWrite: 4}})
	rows := st.snapshot()
	if len(rows) != 1 || rows[0].Requests != 2 || rows[0].InputTokens != 10 || rows[0].CacheWriteTokens != 4 || rows[0].StatusCodes["503"] != 1 {
		t.Fatalf("legacy counters = %+v", rows)
	}
	if buckets := st.snapshotForPersist().Models["a\x00m"].Buckets; len(buckets) != 1 {
		t.Fatalf("expired legacy buckets retained: %+v", buckets)
	}
}

func TestStatsConcurrentSnapshotsAndRecording(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{RetentionDays: 1})
	a := completedAttempt{at: time.Now().Add(-48 * time.Hour), backend: "a", model: "m", status: "503", ttft: 0.2}
	st.recordAttempt(a)
	a.at = time.Now()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				st.recordAttempt(a)
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			for range 100 {
				if _, err := json.Marshal(st.snapshotForPersist()); err != nil {
					t.Error(err)
				}
				st.snapshotFromBuckets()
				if _, _, err := st.seriesAt("1h", a.at); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	row := st.snapshotFromBuckets()[0]
	if row.Requests != 401 || row.StatusCodes["503"] != 401 {
		t.Fatalf("concurrent counters = %+v", row)
	}
	if buckets := st.snapshotForPersist().Models["a\x00m"].Buckets; len(buckets) != 1 || buckets[0].Requests != 400 {
		t.Fatalf("retention buckets = %+v", buckets)
	}
}

func TestStatsTrackerDoneOnce(t *testing.T) {
	mini := miniredis.RunT(t)
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{RedisURL: "redis://" + mini.Addr()})
	t.Cleanup(func() { _ = st.Close() })
	tr := st.track("a", "m")
	st.inspect(tr, "proxy-request", "anthropic")
	tr.setUpstreamStatus(200)
	tr.markBodyFailure()
	tr.noteUpstreamError([]byte(`{"error":{"message":"unavailable"}}`))
	tr.rep = usageReport{input: 5, cacheWrite: 3}
	tr.noteFirstByte()
	tr.done()
	tr.done()

	for name, rows := range map[string][]ModelStat{
		"Redis": st.snapshot(), "memory": st.snapshotFromBuckets(), "Prometheus": st.snapshotFromPrometheus(),
	} {
		if len(rows) != 1 || rows[0].Requests != 1 || rows[0].Successes != 0 || rows[0].StatusCodes["200"] != 1 || rows[0].CacheWriteTokens != 3 {
			t.Fatalf("%s completion recorded incorrectly: %+v", name, rows)
		}
		if rows[0].TTFT != (Percentiles{}) || rows[0].E2E != (Percentiles{}) {
			t.Fatalf("%s body failure recorded latency observations: %+v", name, rows[0])
		}
	}
	requests, errors := st.RecentRequests(), st.RecentUpstreamErrors()
	if len(requests) != 1 || len(errors) != 1 || errors[0].RequestID != requests[0].ID || !errors[0].At.Equal(requests[0].At) {
		t.Fatalf("completion activity differs: requests=%+v errors=%+v", requests, errors)
	}
}
