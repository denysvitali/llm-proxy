package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
			Help: "Extra upstream attempts made after transient failures.",
		}, []string{"phase"}),
		retryOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_upstream_retry_outcomes_total",
			Help: "How transient upstream failures ended.",
		}, []string{"phase", "outcome"}),
		reg: prometheus.NewRegistry(),
	}
	m.reg.MustRegister(m.requests, m.requestDuration, m.authSuccesses, m.authFailures, m.retryAttempts, m.retryOutcomes)
	return m
}

// noteRetryAttempt records one extra upstream attempt after a transient
// failure; phase is "connect" or "body".
func (m *Metrics) noteRetryAttempt(phase string) {
	m.retryAttempts.WithLabelValues(phase).Inc()
}

// noteRetryOutcome records how a transient upstream failure ended:
// "recovered" (a retry succeeded), "exhausted" (retries ran out and the
// client got an error) or "surfaced" (content had already been forwarded,
// so the break was reported as an in-stream failure instead of replayed).
func (m *Metrics) noteRetryOutcome(phase, outcome string) {
	m.retryOutcomes.WithLabelValues(phase, outcome).Inc()
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
