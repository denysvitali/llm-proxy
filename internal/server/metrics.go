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
		reg: prometheus.NewRegistry(),
	}
	m.reg.MustRegister(m.requests, m.requestDuration, m.authSuccesses, m.authFailures)
	return m
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
