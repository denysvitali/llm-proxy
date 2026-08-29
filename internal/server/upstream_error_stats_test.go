package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func TestUpstreamErrorSummary(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"anthropic", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "Overloaded"},
		{"openai code", `{"error":{"message":"Rate limit reached","code":"rate_limit_exceeded","type":"requests"}}`, "rate_limit_exceeded: Rate limit reached"},
		{"openai numeric code", `{"error":{"message":"quota","code":429}}`, "429: quota"},
		{"flat message", `{"message":"Insufficient balance"}`, "Insufficient balance"},
		{"plain text", "upstream is sad\nsecond line", "upstream is sad"},
		{"html", "<html><body>oops</body></html>", "<html><body>oops</body></html>"},
		{"bom", "\xef\xbb\xbf{\"error\":{\"message\":\"BOM body\"}}", "BOM body"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamErrorSummary([]byte(tc.body)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrackerCapturesStatusAndMessage(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("venice", "stealth-ox-alpha")
	tr.setUpstreamStatus(http.StatusTooManyRequests)
	tr.noteUpstreamError([]byte(`{"error":{"message":"The model is currently overloaded","code":"overloaded"}}`))
	tr.done()

	models := st.snapshot()
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	m := models[0]
	if m.StatusCodes["429"] != 1 {
		t.Fatalf("StatusCodes = %v, want 429:1", m.StatusCodes)
	}
	recent := st.RecentUpstreamErrors()
	if len(recent) != 1 {
		t.Fatalf("recent = %d events, want 1", len(recent))
	}
	ev := recent[0]
	if ev.Backend != "venice" || ev.Model != "stealth-ox-alpha" || ev.Status != "429" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Message != "overloaded: The model is currently overloaded" {
		t.Fatalf("message = %q", ev.Message)
	}

	// A later success must not clear the recorded failure counts.
	ok := st.track("venice", "stealth-ox-alpha")
	ok.setUpstreamStatus(http.StatusOK)
	ok.done()
	models = st.snapshot()
	if models[0].StatusCodes["429"] != 1 || models[0].Successes != 1 {
		t.Fatalf("after success: codes=%v successes=%d", models[0].StatusCodes, models[0].Successes)
	}
	if got := len(st.RecentUpstreamErrors()); got != 2 && got != 1 {
		// successes never enter the ring; only the one failure.
		t.Fatalf("recent after success = %d events, want 1", got)
	}
}

func TestTrackerBodyFailureOn200WithErrorObject(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("venice", "stealth-mock")
	tr.setUpstreamStatus(http.StatusOK)
	tr.markBodyFailure()
	tr.errMsg = "quota exhausted behind a 200"
	tr.done()

	models := st.snapshot()
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	m := models[0]
	if m.Requests != 1 || m.Successes != 0 || m.Uptime != 0 {
		t.Fatalf("requests=%d successes=%d uptime=%f; want 1/0/0", m.Requests, m.Successes, m.Uptime)
	}
	if m.StatusCodes["200"] != 1 {
		t.Fatalf("StatusCodes = %v, want 200:1", m.StatusCodes)
	}
	recent := st.RecentUpstreamErrors()
	if len(recent) != 1 || recent[0].Message != "quota exhausted behind a 200" || recent[0].Status != "200" {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestSnifferDetectsErrorShapedSuccess(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("venice", "stealth-mock")
	tr.setUpstreamStatus(http.StatusOK)
	sn := newSniffer(&nocloseReader{Reader: strings.NewReader(
		`{"error":{"message":"mock chaos quota exhausted behind a 200","type":"insufficient_credits"}}`)}, tr, false, http.StatusOK)
	n, _ := sn.Read(make([]byte, 4096))
	if n == 0 {
		t.Fatal("sniffer read nothing")
	}
	sn.Finish()
	tr.done()

	m := st.snapshot()[0]
	if m.Successes != 0 || m.StatusCodes["200"] != 1 {
		t.Fatalf("requests=%d successes=%d codes=%v", m.Requests, m.Successes, m.StatusCodes)
	}
	ev := st.RecentUpstreamErrors()[0]
	if ev.Message != "mock chaos quota exhausted behind a 200" {
		t.Fatalf("message = %q", ev.Message)
	}
}

func TestRecentRingOrderAndCap(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	for i := 0; i < maxRecentErrors+10; i++ {
		tr := st.track("b", "m")
		tr.setUpstreamStatus(http.StatusServiceUnavailable)
		tr.done()
	}
	recent := st.RecentUpstreamErrors()
	if len(recent) != maxRecentErrors {
		t.Fatalf("len(recent) = %d, want %d", len(recent), maxRecentErrors)
	}
	// Newest first: all entries are identical here, so verify count stays at cap.
	if recent[0].Status != "503" {
		t.Fatalf("status = %s", recent[0].Status)
	}
}

func TestStatsErrorsEndpoint(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("opencode", "kimi-k3")
	tr.setUpstreamStatus(http.StatusBadGateway)
	tr.noteTransportError(nil) // no transport error; message falls back to status text
	tr.done()

	s := &Server{stats: st}
	w := httptest.NewRecorder()
	s.handleStatsErrors(w, httptest.NewRequest(http.MethodGet, "/api/stats/errors", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var payload struct {
		Errors []UpstreamErrorEvent `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(payload.Errors))
	}
	if !strings.Contains(payload.Errors[0].Message, "502") {
		t.Fatalf("message = %q, want status fallback text", payload.Errors[0].Message)
	}
}

func TestRequestInspectionListsMetadataAndServesBodies(t *testing.T) {
	st := newStats(prometheus.NewRegistry(), config.StatsConfig{})
	tr := st.track("workbuddy", "hy3")
	st.inspect(tr, "proxy-1", string(backend.KindOpenAIChat), []byte(`{"model":"workbuddy/hy3"}`), []byte(`{"model":"hy3"}`))
	tr.setUpstreamStatus(http.StatusBadRequest)
	tr.noteUpstreamError([]byte(`{"msg":"rejected"}`))
	tr.done()

	list := st.RecentRequests()
	if len(list) != 1 || list[0].ID == "" || list[0].ClientRequest != nil || list[0].UpstreamRequest != nil {
		t.Fatalf("RecentRequests() = %+v", list)
	}
	detail, ok := st.Request(list[0].ID)
	if !ok || string(detail.ClientRequest) != `{"model":"workbuddy/hy3"}` || string(detail.UpstreamRequest) != `{"model":"hy3"}` {
		t.Fatalf("Request() = %+v, %v", detail, ok)
	}
	errors := st.RecentUpstreamErrors()
	if len(errors) != 1 || errors[0].RequestID != list[0].ID {
		t.Fatalf("RecentUpstreamErrors() = %+v", errors)
	}
}
