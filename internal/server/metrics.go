package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// Metrics aggregates proxy-wide Prometheus counters and latency histograms.
// Each Server owns a private registry so multiple servers (and tests) can
// coexist in one process without duplicate-registration panics.
type Metrics struct {
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	authSuccesses   *prometheus.CounterVec
	authFailures    prometheus.Counter
	retryAttempts   *prometheus.CounterVec
	retryOutcomes   *prometheus.CounterVec
	fallbacks       *prometheus.CounterVec
	reg             *prometheus.Registry
}

func newMetrics() *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_requests_total",
			Help: "Requests handled, by method, path, and status.",
		}, []string{"method", "path", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "llm_proxy_request_duration_seconds",
			Help:    "Request latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 14),
		}, []string{"method", "path"}),
		authSuccesses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_auth_success_total",
			Help: "Successful API-key authentications, by user.",
		}, []string{"user"}),
		authFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "llm_proxy_auth_failure_total",
			Help: "Rejected API-key authentications.",
		}),
		retryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_upstream_retry_attempts_total",
			Help: "Extra upstream attempts made after transient failures, by backend and model.",
		}, []string{"phase", "backend", "model"}),
		retryOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_upstream_retry_outcomes_total",
			Help: "How transient upstream failures ended, by backend and model.",
		}, []string{"phase", "outcome", "backend", "model"}),
		fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_fallbacks_total",
			Help: "Requests handed from a failing backend to a fallback backend.",
		}, []string{"from_backend", "to_backend"}),
		reg: prometheus.NewRegistry(),
	}
	m.reg.MustRegister(m.requests, m.requestDuration, m.authSuccesses, m.authFailures, m.retryAttempts, m.retryOutcomes, m.fallbacks)
	return m
}

// noteRetryAttempt records one extra upstream attempt after a transient
// failure; phase is "connect" or "body".
func (m *Metrics) noteRetryAttempt(phase, backend, model string) {
	m.retryAttempts.WithLabelValues(phase, backend, model).Inc()
}

// noteRetryOutcome records how a transient upstream failure ended:
// "recovered" (a retry succeeded), "exhausted" (retries ran out and the
// client got an error) or "surfaced" (content had already been forwarded,
// so the break was reported as an in-stream failure instead of replayed).
func (m *Metrics) noteRetryOutcome(phase, outcome, backend, model string) {
	m.retryOutcomes.WithLabelValues(phase, outcome, backend, model).Inc()
}

// noteFallback records one request moving from a backend that failed before
// any output to the next backend in its fallback chain.
func (m *Metrics) noteFallback(from, to string) {
	m.fallbacks.WithLabelValues(from, to).Inc()
}

// sumRetryOutcomes totals the outcome counter across every backend and model;
// tests use it to assert deltas without pinning label tuples.
func (m *Metrics) sumRetryOutcomes(phase, outcome string) float64 {
	var total float64
	ch := make(chan prometheus.Metric, 16)
	go func() {
		m.retryOutcomes.Collect(ch)
		close(ch)
	}()
	for metric := range ch {
		sample := &dto.Metric{}
		if err := metric.Write(sample); err != nil {
			continue
		}
		matched := true
		for _, lp := range sample.Label {
			if (lp.GetName() == "phase" && lp.GetValue() != phase) ||
				(lp.GetName() == "outcome" && lp.GetValue() != outcome) {
				matched = false
				break
			}
		}
		if matched {
			total += sample.GetCounter().GetValue()
		}
	}
	return total
}

func (m *Metrics) observe(method, path string, status int, d time.Duration) {
	code := status
	if code == 0 {
		code = http.StatusOK
	}
	m.requests.WithLabelValues(method, path, strconv.Itoa(code)).Inc()
	m.requestDuration.WithLabelValues(method, path).Observe(d.Seconds())
}

func (m *Metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
