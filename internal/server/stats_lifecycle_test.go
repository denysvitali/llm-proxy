package server

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func TestTrackerAttemptPhaseTransitions(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("b", "m")
	if got := tr.phase(); got != attemptPending {
		t.Fatalf("new tracker phase = %v", got)
	}
	tr.noteFirstByte()
	first := tr.firstAt.Load()
	if got := tr.phase(); got != attemptFirstByte || first == 0 {
		t.Fatalf("after first byte phase = %v stamp = %d", got, first)
	}
	time.Sleep(time.Millisecond)
	tr.noteFirstByte()
	if tr.firstAt.Load() != first {
		t.Fatal("second byte moved the TTFT stamp")
	}
	tr.setUpstreamStatus(200)
	tr.done()
	if got := tr.phase(); got != attemptDone {
		t.Fatalf("after done phase = %v", got)
	}
	tr.done()
	if snap := st.snapshot(); len(snap) != 1 || snap[0].Requests != 1 {
		t.Fatalf("done twice recorded %+v", snap)
	}
	if attemptPhase(9).String() != "attemptPhase(9)" || attemptDone.String() != "done" {
		t.Fatal("attemptPhase String")
	}
}

func TestTrackerDoneFromPending(t *testing.T) {
	tr := newStats(prometheus.NewRegistry(), config.StatsConfig{}).track("b", "m")
	if tr.succeeded() {
		t.Fatal("transport error counted as success")
	}
	tr.done()
	if tr.phase() != attemptDone {
		t.Fatalf("phase = %v", tr.phase())
	}
	tr.noteFirstByte()
	if tr.phase() != attemptDone {
		t.Fatal("late first byte left the done state")
	}
}

func TestEvictionMarkClaim(t *testing.T) {
	var m evictionMark
	if m.claim(0) {
		t.Fatal("zero mark claimed window 0")
	}
	if !m.claim(5) || m != 5 {
		t.Fatalf("claim(5) on fresh mark: %d", m)
	}
	if m.claim(5) {
		t.Fatal("same window claimed twice")
	}
	if !m.claim(6) || m != 6 {
		t.Fatal("next window not claimed")
	}
}

func TestPersistLoopTransitions(t *testing.T) {
	var l persistLoop
	if l.halt() || l.state != persistIdle {
		t.Fatal("halt on idle loop asked for a flush")
	}
	stop, ok := l.begin()
	if !ok || l.state != persistRunning {
		t.Fatal("begin from idle failed")
	}
	if _, again := l.begin(); again {
		t.Fatal("begin twice succeeded")
	}
	if !l.halt() || l.state != persistStopped {
		t.Fatal("halt on running loop")
	}
	select {
	case <-stop:
	default:
		t.Fatal("stop channel not closed")
	}
	if !l.halt() {
		t.Fatal("repeated halt must still flush")
	}
	if persistStopped.String() != "stopped" {
		t.Fatal("persistLoopState String")
	}
}

func TestStatsStorageModeDerivation(t *testing.T) {
	mini := miniredis.RunT(t)
	cases := []struct {
		cfg  config.StatsConfig
		want statsStorageMode
	}{
		{config.StatsConfig{}, storageMetricsOnly},
		{config.StatsConfig{PersistFile: t.TempDir() + "/s.json"}, storageFile},
		{config.StatsConfig{RedisURL: "redis://" + mini.Addr(), PersistFile: "ignored.json"}, storageShared},
		{config.StatsConfig{RedisURL: "invalid://host"}, storageSharedFailed},
	}
	for _, tc := range cases {
		st := newStats(prometheus.NewRegistry(), tc.cfg)
		if got := st.storageMode(); got != tc.want {
			t.Errorf("%+v: mode = %v, want %v", tc.cfg, got, tc.want)
		}
		_ = st.Close()
	}
	if storageSharedFailed.String() != "shared-failed" {
		t.Fatal("statsStorageMode String")
	}
}

type closeCounter struct {
	io.Reader
	closes int
}

func (c *closeCounter) Close() error { c.closes++; return nil }

func TestSnifferOverflowAndClose(t *testing.T) {
	tr := newStats(prometheus.NewRegistry(), config.StatsConfig{}).track("b", "m")
	body := &closeCounter{Reader: strings.NewReader("x")}
	sn := newSniffer(body, tr, false, 200)
	sn.retain(make([]byte, sniffCap))
	if sn.state != sniffBuffering || sn.buf.Len() != sniffCap {
		t.Fatalf("at cap: state=%v len=%d", sn.state, sn.buf.Len())
	}
	if _, err := io.ReadAll(sn); err != nil {
		t.Fatal(err)
	}
	if sn.state != sniffOverflowed || sn.buf.Len() != 0 {
		t.Fatalf("past cap: state=%v len=%d", sn.state, sn.buf.Len())
	}
	sn.retain([]byte("y"))
	if sn.buf.Len() != 0 {
		t.Fatal("overflowed sniffer retained bytes")
	}
	_ = sn.Close()
	_ = sn.Close()
	if body.closes != 1 {
		t.Fatalf("closes = %d", body.closes)
	}
}

func TestSnifferVerdict(t *testing.T) {
	tr := newStats(prometheus.NewRegistry(), config.StatsConfig{}).track("b", "m")
	cases := []struct {
		status int
		sse    bool
		body   string
		want   sniffVerdict
	}{
		{200, false, "", sniffNoBody},
		{0, false, `{}`, sniffFailed},
		{503, true, `data: {}`, sniffFailed},
		{200, true, `data: {"type":"error"}`, sniffStream},
		{200, false, `{"error":{"message":"quota"}}`, sniffErrorBody},
		{200, false, `{"id":"x"}`, sniffOK},
	}
	for _, tc := range cases {
		sn := newSniffer(io.NopCloser(strings.NewReader("")), tr, tc.sse, tc.status)
		if got := sn.verdict([]byte(tc.body)); got != tc.want {
			t.Errorf("status=%d sse=%v body=%q: verdict %v, want %v", tc.status, tc.sse, tc.body, got, tc.want)
		}
	}
	if sniffErrorBody.String() != "error-body" || sniffOverflowed.String() != "overflowed" {
		t.Fatal("sniff String")
	}
}

func TestUpdateHubStateTransitions(t *testing.T) {
	h := newUpdateHub()
	ch, unsub := h.subscribe()
	h.close()
	if h.state != hubClosed || len(h.subs) != 0 {
		t.Fatalf("after close: state=%v subs=%d", h.state, len(h.subs))
	}
	if _, open := <-ch; open {
		t.Fatal("hub close left a subscriber open")
	}
	unsub() // released already; must not double-close
	h.close()
	late, _ := h.subscribe()
	if _, open := <-late; open {
		t.Fatal("closed hub handed out an open channel")
	}
	h.notify(statsUpdatedEvent)
	if hubOpen.String() != "open" {
		t.Fatal("hubState String")
	}
}

func TestRedisLifecycleTransitions(t *testing.T) {
	mini := miniredis.RunT(t)
	r, err := newRedisStats("redis://"+mini.Addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.state != redisReady {
		t.Fatalf("new state = %v", r.state)
	}
	r.startUpdates(func([]byte) {})
	if r.state != redisListening {
		t.Fatalf("after startUpdates = %v", r.state)
	}
	r.close()
	r.close()
	if r.state != redisClosed {
		t.Fatalf("after close = %v", r.state)
	}
	select {
	case <-r.stop:
	default:
		t.Fatal("stop not signalled")
	}
	if r.client.Ping(t.Context()).Err() == nil {
		t.Fatal("client still open")
	}
	if redisClosed.String() != "closed" {
		t.Fatal("redisLifecycle String")
	}
}
