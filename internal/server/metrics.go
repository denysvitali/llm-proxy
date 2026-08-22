package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics aggregates proxy-wide Prometheus counters and latency histograms.
type Metrics struct {
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	authSuccesses   *prometheus.CounterVec
	authFailures    prometheus.Counter
	reg             prometheus.Registerer
}

func newMetrics() *Metrics {
	reg := prometheus.DefaultRegisterer
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
		reg: reg,
	}
	reg.MustRegister(m.requests, m.requestDuration, m.authSuccesses, m.authFailures)
	return m
}

func (m *Metrics) observe(method, path string, status int, d time.Duration) {
	code := status
	if code == 0 {
		code = http.StatusOK
	}
	m.requests.WithLabelValues(method, path, itoa(code)).Inc()
	m.requestDuration.WithLabelValues(method, path).Observe(d.Seconds())
}

func (m *Metrics) handler() http.Handler {
	return promhttp.Handler()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
