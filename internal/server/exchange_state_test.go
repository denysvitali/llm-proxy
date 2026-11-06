package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/translate"
)

func TestExchangeResultTransitions(t *testing.T) {
	dialect := openAIDialect()

	handoff := failedExchange("boom")
	if handoff.stage() != exchangeStageHandoff || !handoff.canAdvanceChain() {
		t.Fatalf("failedExchange stage = %v, want handoff", handoff.stage())
	}

	final := failedExchange("boom")
	rec := httptest.NewRecorder()
	final.answerFinal(rec, dialect)
	if final.stage() != exchangeStageCommitted || final.fallbackEligible || rec.Code != http.StatusBadGateway {
		t.Fatalf("answerFinal: stage=%v eligible=%t code=%d", final.stage(), final.fallbackEligible, rec.Code)
	}
	// A committed result cannot be answered twice.
	rec2 := httptest.NewRecorder()
	final.answerFinal(rec2, dialect)
	final.answerRejection(rec2, dialect)
	if rec2.Body.Len() != 0 {
		t.Fatalf("committed result wrote again: %s", rec2.Body.String())
	}

	rejected := exchangeResult{outcome: exchangeRejected, message: "bad", rejection: &translate.UpstreamError{Message: "bad"}}
	if rejected.stage() != exchangeStageHeld || rejected.canAdvanceChain() {
		t.Fatalf("rejection stage = %v, want held", rejected.stage())
	}
	rec = httptest.NewRecorder()
	rejected.answerFinal(rec, dialect) // held: no 502
	if rec.Body.Len() != 0 {
		t.Fatalf("held result answered final: %s", rec.Body.String())
	}
	rejected.answerRejection(rec, dialect)
	if rejected.stage() != exchangeStageCommitted || rec.Code != http.StatusBadRequest {
		t.Fatalf("answerRejection: stage=%v code=%d", rejected.stage(), rec.Code)
	}

	canceled := failedExchange("boom")
	canceled.cancel()
	if canceled.outcome != exchangeCanceled || canceled.stage() != exchangeStageHeld {
		t.Fatalf("cancel: outcome=%v stage=%v", canceled.outcome, canceled.stage())
	}
	served := servedExchange(true)
	served.cancel()
	if served.outcome != exchangeOK || served.stage() != exchangeStageCommitted {
		t.Fatalf("cancel changed a served result: %+v", served)
	}
	if exchangeOutcomeName(exchangeSurfaced) != "surfaced" || exchangeOutcome(99).String() != "unknown" {
		t.Fatal("outcome names changed")
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestGatedWriterStates(t *testing.T) {
	var buf bytes.Buffer
	opened := 0
	g := &gatedWriter{open: func() io.Writer { opened++; return &buf }}
	if g.committed() || g.failed() || g.state != gateUncommitted {
		t.Fatalf("new gate state = %v", g.state)
	}
	if n, err := g.Write(nil); n != 0 || err != nil || g.committed() {
		t.Fatalf("empty write committed the gate: %v", g.state)
	}
	if _, err := g.Write([]byte("a")); err != nil || g.state != gateCommitted {
		t.Fatalf("first write: err=%v state=%v", err, g.state)
	}
	_, _ = g.Write([]byte("b"))
	if opened != 1 || buf.String() != "ab" || g.wrote != 2 {
		t.Fatalf("opened=%d buf=%q wrote=%d", opened, buf.String(), g.wrote)
	}

	boom := errors.New("boom")
	f := &gatedWriter{open: func() io.Writer { return failingWriter{err: boom} }}
	if _, err := f.Write([]byte("x")); !errors.Is(err, boom) || f.state != gateFailed || !f.committed() {
		t.Fatalf("failed write: err=%v state=%v", err, f.state)
	}
	if _, err := f.Write([]byte("y")); !errors.Is(err, boom) {
		t.Fatalf("failure not sticky: %v", err)
	}
	f.commit()
	if f.state != gateFailed || gatedWriterState(9).String() != "unknown" {
		t.Fatalf("failed gate left its terminal state: %v", f.state)
	}
}

func TestRetryTrackerStates(t *testing.T) {
	m := newMetrics()
	newTracker := func(phase upstreamPhase) *retryTracker {
		return &retryTracker{metrics: m, phase: phase, backend: "b", model: "m"}
	}
	outcome := func(phase upstreamPhase, st retryState) float64 {
		return m.sumRetryOutcomes(phase.String(), st.String())
	}
	if upstreamPhaseConnect.String() != retryPhaseConnect || upstreamPhaseBody.String() != retryPhaseBody {
		t.Fatal("phase labels changed")
	}

	first := newTracker(upstreamPhaseBody)
	first.succeeded()
	if first.state != retryStateUnneeded || outcome(upstreamPhaseBody, retryStateRecovered) != 0 {
		t.Fatalf("first-try success = %v", first.state)
	}

	recovered := newTracker(upstreamPhaseBody)
	recovered.retried()
	recovered.succeeded()
	recovered.exhausted() // terminal: ignored
	if recovered.state != retryStateRecovered || recovered.retries != 1 ||
		outcome(upstreamPhaseBody, retryStateRecovered) != 1 || outcome(upstreamPhaseBody, retryStateExhausted) != 0 {
		t.Fatalf("recovered tracker = %v", recovered.state)
	}

	surfaced := newTracker(upstreamPhaseBody)
	surfaced.surfaced()
	if surfaced.state != retryStateSurfaced || outcome(upstreamPhaseBody, retryStateSurfaced) != 1 {
		t.Fatalf("surfaced tracker = %v", surfaced.state)
	}

	connect := newTracker(upstreamPhaseConnect)
	connect.concluded(true)
	if connect.state != retryStateUnneeded || outcome(upstreamPhaseConnect, retryStateExhausted) != 0 {
		t.Fatalf("connect without retries = %v", connect.state)
	}
	connect = newTracker(upstreamPhaseConnect)
	connect.retried()
	connect.concluded(true)
	if connect.state != retryStateExhausted || outcome(upstreamPhaseConnect, retryStateExhausted) != 1 {
		t.Fatalf("connect exhausted = %v", connect.state)
	}

	for _, end := range []func(*retryTracker){(*retryTracker).rejected, (*retryTracker).abandoned} {
		tr := newTracker(upstreamPhaseBody)
		tr.retried()
		end(tr)
		tr.retried()
		if tr.retries != 1 {
			t.Fatalf("terminal tracker %v kept retrying", tr.state)
		}
	}
	if outcome(upstreamPhaseBody, retryStateRecovered)+outcome(upstreamPhaseBody, retryStateExhausted) != 1 {
		t.Fatal("rejected/abandoned recorded an outcome")
	}
}

func TestStatusRecorderStates(t *testing.T) {
	rec := &statusRecorder{delegate: httptest.NewRecorder()}
	if rec.state() != recorderPending {
		t.Fatalf("new recorder = %v", rec.state())
	}
	rec.WriteHeader(http.StatusEarlyHints)
	if rec.state() != recorderPending {
		t.Fatal("1xx committed the recorder")
	}
	rec.WriteHeader(http.StatusTeapot)
	rec.WriteHeader(http.StatusOK)
	if rec.state() != recorderCommitted || rec.status != http.StatusTeapot {
		t.Fatalf("status = %d state = %v", rec.status, rec.state())
	}

	upgrade := &statusRecorder{delegate: httptest.NewRecorder()}
	upgrade.WriteHeader(http.StatusSwitchingProtocols)
	if upgrade.status != http.StatusSwitchingProtocols {
		t.Fatalf("101 status = %d", upgrade.status)
	}

	for name, act := range map[string]func(*statusRecorder){
		"write":  func(r *statusRecorder) { _, _ = r.Write([]byte("x")) },
		"flush":  (*statusRecorder).Flush,
		"settle": (*statusRecorder).settle,
	} {
		r := &statusRecorder{delegate: httptest.NewRecorder()}
		act(r)
		if r.state() != recorderCommitted || r.status != http.StatusOK {
			t.Fatalf("%s: status = %d", name, r.status)
		}
	}
	settled := &statusRecorder{delegate: httptest.NewRecorder(), status: http.StatusNotFound}
	settled.settle()
	if settled.status != http.StatusNotFound || recorderState(7).String() != "unknown" {
		t.Fatalf("settle overwrote status %d", settled.status)
	}
}
