package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func activityStats(t *testing.T, mini *miniredis.Miniredis, prefix string) *Stats {
	t.Helper()
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{
		RedisURL: "redis://" + mini.Addr(), RedisKeyPrefix: prefix,
	})
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func recordActivity(st *Stats, at time.Time, success bool) InspectedRequest {
	tr := st.track("provider", "model")
	st.inspect(tr, "proxy-request", "chat")
	status, message := "200", ""
	if !success {
		status, message = "503", "provider unavailable"
	}
	st.recordAttempt(completedAttempt{
		at: at, backend: "provider", model: "model", status: status,
		success: success, message: message, request: tr.request,
	})
	tr.request.At, tr.request.Status, tr.request.Error = at, status, message
	return tr.request
}

func activityResponse(st *Stats, path, id string, ctx context.Context) *httptest.ResponseRecorder {
	s := &Server{stats: st}
	r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	switch path {
	case "/api/stats/errors":
		s.handleStatsErrors(w, r)
	case "/api/requests":
		s.handleRequests(w, r)
	default:
		s.handleRequest(w, r)
	}
	return w
}

func TestRedisActivityAPIsSharedAndTimestampOrdered(t *testing.T) {
	mini := miniredis.RunT(t)
	first, second := activityStats(t, mini, "fleet:"), activityStats(t, mini, "fleet:")
	at := time.Now().UTC()
	// Delayed commits and timestamp ties must produce the same ordering on
	// either reader, without rounding nanosecond differences away.
	requests := []InspectedRequest{
		recordActivity(first, at.Add(3*time.Nanosecond), false),
		recordActivity(second, at, false),
		recordActivity(first, at.Add(time.Nanosecond), false),
		recordActivity(second, at.Add(time.Nanosecond), false),
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].At.Equal(requests[j].At) {
			return requests[i].ID > requests[j].ID
		}
		return requests[i].At.After(requests[j].At)
	})
	for _, reader := range []*Stats{second, first, second} {
		w := activityResponse(reader, "/api/requests", "", context.Background())
		var list struct {
			Requests []InspectedRequest `json:"requests"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || !reflect.DeepEqual(list.Requests, requests) {
			t.Fatalf("shared requests: status=%d body=%s want=%+v", w.Code, w.Body.String(), requests)
		}
		w = activityResponse(reader, "/api/stats/errors", "", context.Background())
		var feed struct {
			Errors []UpstreamErrorEvent `json:"errors"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &feed) != nil || len(feed.Errors) != len(requests) {
			t.Fatalf("shared failures: status=%d body=%s", w.Code, w.Body.String())
		}
		for i, want := range requests {
			if feed.Errors[i].RequestID != want.ID || !feed.Errors[i].At.Equal(want.At) || feed.Errors[i].Message != want.Error {
				t.Fatalf("failure %d = %+v want=%+v", i, feed.Errors[i], want)
			}
			w = activityResponse(reader, "/api/requests/"+want.ID, want.ID, context.Background())
			var detail InspectedRequest
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || !reflect.DeepEqual(detail, want) {
				t.Fatalf("shared detail: status=%d body=%s want=%+v", w.Code, w.Body.String(), want)
			}
		}
	}
}

func TestRedisActivityRetentionPreservesFailureDetails(t *testing.T) {
	mini := miniredis.RunT(t)
	first, second := activityStats(t, mini, "fleet:"), activityStats(t, mini, "fleet:")
	at := time.Now().UTC()
	failure := recordActivity(first, at, false)
	for i := range maxRecentErrors + 10 {
		recordActivity(second, at.Add(time.Duration(i+1)*time.Second), true)
	}
	feed, err := second.recentRequests(context.Background())
	if err != nil || len(feed) != maxRecentErrors {
		t.Fatalf("bounded requests: len=%d err=%v", len(feed), err)
	}
	for _, req := range feed {
		if req.ID == failure.ID {
			t.Fatal("success churn did not evict the old request from the request feed")
		}
	}
	detail, ok, err := second.request(context.Background(), failure.ID)
	if err != nil || !ok || !reflect.DeepEqual(detail, failure) {
		t.Fatalf("retained failure detail: %+v ok=%v err=%v", detail, ok, err)
	}
	// Successes leave failures intact; only later failures expire this detail.
	for i := range maxRecentErrors + 10 {
		recordActivity(first, at.Add(time.Duration(100+i)*time.Second), false)
	}
	// A late commit for an older completion cannot displace newer failures.
	late := recordActivity(second, at.Add(-time.Second), false)
	events, err := second.recentUpstreamErrors(context.Background())
	if err != nil || len(events) != maxRecentErrors {
		t.Fatalf("bounded failures: len=%d err=%v", len(events), err)
	}
	for _, event := range events {
		if event.RequestID == late.ID {
			t.Fatal("delayed old completion displaced a newer failure")
		}
		w := activityResponse(second, "/api/requests/"+event.RequestID, event.RequestID, context.Background())
		if w.Code != http.StatusOK {
			t.Fatalf("retained failure lacks inspection: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	w := activityResponse(second, "/api/requests/"+failure.ID, failure.ID, context.Background())
	if w.Code != http.StatusNotFound {
		t.Fatalf("evicted detail: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, key := range []string{first.redis.requestsKey(), first.redis.errorsKey()} {
		n, err := first.redis.client.ZCard(context.Background(), key).Result()
		if err != nil || n != maxRecentErrors {
			t.Fatalf("unbounded activity key %s: %d", key, n)
		}
	}
}

func TestRedisActivityNamespaceIsolationAndEmptyShape(t *testing.T) {
	mini := miniredis.RunT(t)
	first, isolated := activityStats(t, mini, "first:"), activityStats(t, mini, "isolated:")
	local := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	for _, st := range []*Stats{first, isolated, local} {
		for path, want := range map[string]string{"/api/stats/errors": `{"errors":[]}`, "/api/requests": `{"requests":[]}`} {
			w := activityResponse(st, path, "", context.Background())
			if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
				t.Fatalf("empty %s: status=%d body=%s", path, w.Code, w.Body.String())
			}
		}
	}
	req := recordActivity(first, time.Now(), false)
	for path, want := range map[string]string{"/api/stats/errors": `{"errors":[]}`, "/api/requests": `{"requests":[]}`} {
		w := activityResponse(isolated, path, "", context.Background())
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
			t.Fatalf("namespace leak %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	w := activityResponse(isolated, "/api/requests/"+req.ID, req.ID, context.Background())
	if w.Code != http.StatusNotFound {
		t.Fatalf("namespace detail leak: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRedisActivityReadFailuresReturn503(t *testing.T) {
	for _, failure := range []string{"init", "unavailable", "wrong request type", "wrong error type", "request decode", "error decode", "error missing inspection"} {
		t.Run(failure, func(t *testing.T) {
			mini := miniredis.RunT(t)
			st := activityStats(t, mini, "fleet:")
			req := recordActivity(st, time.Now(), false)
			errorFails, requestFails := true, true
			switch failure {
			case "init":
				st.redis.close()
				st.redis, st.redisInitErr = newRedisStats("invalid://host", "fleet:")
			case "unavailable":
				st.redis.close()
			case "wrong request type", "wrong error type":
				key := st.redis.requestsKey()
				if failure == "wrong error type" {
					key, requestFails = st.redis.errorsKey(), false
				} else {
					errorFails = false
				}
				mini.Del(key)
				if err := mini.Set(key, "not a sorted set"); err != nil {
					t.Fatal(err)
				}
			case "request decode", "error decode", "error missing inspection":
				key, member := st.redis.requestsKey(), "99999999999999999999:id\n{invalid"
				if failure != "request decode" {
					key, requestFails = st.redis.errorsKey(), false
				} else {
					errorFails = false
				}
				if failure == "error missing inspection" {
					member = `99999999999999999999:id` + "\n" + `{"error":{"at":"2026-10-07T00:00:00Z","request_id":"missing"}}`
				}
				if _, err := mini.ZAdd(key, 0, member); err != nil {
					t.Fatal(err)
				}
			}
			for _, call := range []struct {
				path string
				id   string
				fail bool
			}{
				{"/api/stats/errors", "", errorFails}, {"/api/requests", "", requestFails},
				{"/api/requests/" + req.ID, req.ID, true}, {"/api/requests/missing", "missing", true},
			} {
				w := activityResponse(st, call.path, call.id, context.Background())
				want := http.StatusOK
				if call.fail {
					want = http.StatusServiceUnavailable
				}
				if w.Code != want || (call.fail && (strings.Contains(w.Body.String(), req.ID) || strings.Contains(w.Body.String(), "provider unavailable"))) {
					t.Fatalf("%s: status=%d body=%s want=%d", call.path, w.Code, w.Body.String(), want)
				}
			}
		})
	}
}

type activityContextHook struct {
	deadline time.Time
	wait     bool
}

func (h *activityContextHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *activityContextHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "zrevrange" {
			h.deadline, _ = ctx.Deadline()
			if h.wait {
				<-ctx.Done()
				return ctx.Err()
			}
		}
		return next(ctx, cmd)
	}
}
func (h *activityContextHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if cmd.Name() == "zrevrange" {
				h.deadline, _ = ctx.Deadline()
			}
		}
		return next(ctx, cmds)
	}
}

func TestRedisActivityReadsHonorCancellationAndBoundedDeadline(t *testing.T) {
	st := activityStats(t, miniredis.RunT(t), "fleet:")
	hook := &activityContextHook{}
	st.redis.client.AddHook(hook)
	for _, path := range []string{"/api/stats/errors", "/api/requests", "/api/requests/missing"} {
		hook.deadline = time.Time{}
		start := time.Now()
		w := activityResponse(st, path, "missing", context.Background())
		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			t.Fatalf("read failed: %d %s", w.Code, w.Body.String())
		}
		if hook.deadline.IsZero() || hook.deadline.After(start.Add(time.Second+100*time.Millisecond)) {
			t.Fatalf("unbounded Redis read deadline: %v", hook.deadline)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w = activityResponse(st, path, "missing", ctx)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("cancelled read: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	hook.wait = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_, err := st.recentUpstreamErrors(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !hook.deadline.Equal(deadline) {
		t.Fatalf("caller deadline was not preserved: deadline=%v err=%v", hook.deadline, err)
	}
}

func TestRedisActivityIsReadableBeforeUpdateNotification(t *testing.T) {
	mini := miniredis.RunT(t)
	first, second := activityStats(t, mini, "fleet:"), activityStats(t, mini, "fleet:")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	subscriber := second.redis.client.Subscribe(ctx, second.redis.updatesKey())
	defer func() { _ = subscriber.Close() }()
	if _, err := subscriber.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	req := recordActivity(first, time.Now().UTC(), false)
	if _, err := subscriber.ReceiveMessage(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := second.recentUpstreamErrors(ctx)
	if err != nil || len(events) != 1 || events[0].RequestID != req.ID {
		t.Fatalf("notification preceded shared feed: events=%+v err=%v", events, err)
	}
	detail, ok, err := second.request(ctx, req.ID)
	if err != nil || !ok || !reflect.DeepEqual(detail, req) {
		t.Fatalf("notification preceded shared inspection: detail=%+v ok=%v err=%v", detail, ok, err)
	}
	rows := second.snapshot()
	if len(rows) != 1 || rows[0].Requests != 1 || rows[0].StatusCodes["503"] != 1 {
		t.Fatalf("notification preceded shared counters: %+v", rows)
	}
}
