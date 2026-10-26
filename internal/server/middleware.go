package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder captures the response status for logging and metrics.
type statusRecorder struct {
	delegate http.ResponseWriter
	status   int
	bytes    int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status != 0 {
		return
	}
	// Informational responses do not commit the final response status.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		r.delegate.WriteHeader(code)
		return
	}
	r.status = code
	r.delegate.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.delegate.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Header() http.Header {
	return r.delegate.Header()
}

// Flush promotes streaming so SSE handlers flush through the wrapper.
func (r *statusRecorder) Flush() {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.delegate.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the original ResponseWriter so connection-upgrade handlers can
// reach its Hijacker through wrapped middleware.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.delegate
}

// withMiddleware wraps the mux with panic recovery, request IDs, tracing,
// access logging, metrics, body limits, and API-key authentication.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-Id", requestID)
		rec := &statusRecorder{delegate: w}
		// Extract only W3C trace context; external baggage is not needed here.
		parent := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		parent = context.WithValue(parent, requestIDKey{}, requestID)
		r = r.WithContext(parent)
		route := "unmatched"
		if mux, ok := next.(*http.ServeMux); ok {
			_, pattern := mux.Handler(r)
			if pattern != "" {
				route = routePath(pattern)
			}
		}
		quiet := isProbePath(r.URL.Path) || r.URL.Path == "/metrics"
		startSpan := func() trace.Span {
			ctx, span := tracer.Start(parent, "llm-proxy request",
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithTimestamp(start),
				trace.WithAttributes(
					attribute.String("http.request.method", metricMethod(r.Method)),
					attribute.String("url.path", r.URL.Path),
					attribute.String("http.route", route),
					attribute.String("llm_proxy.request_id", requestID),
				))
			r = r.WithContext(ctx)
			return span
		}
		var span trace.Span
		if !quiet {
			span = startSpan()
		}

		defer func() {
			p := recover()
			if p != nil {
				s.log.WithField("request_id", requestID).
					Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, p)
				if rec.status == 0 {
					writeError(rec, r, http.StatusInternalServerError, "api_error", "internal proxy error")
				}
			}
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			duration := time.Since(start)
			s.metrics.observe(r.Method, route, rec.status, duration)
			if quiet && rec.status < http.StatusBadRequest && p == nil {
				return
			}
			// Probe/scrape spans are created only on failure, with the original
			// start time and parent. No response buffering or success spans needed.
			if span == nil {
				span = startSpan()
			}
			span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
			if p != nil {
				span.RecordError(fmt.Errorf("panic: %v", p))
				span.SetStatus(codes.Error, "handler panic")
			} else if rec.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
			span.End()
			fields := map[string]any{
				"request_id": requestID,
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     rec.status,
				"bytes":      rec.bytes,
				"duration":   duration.String(),
			}
			// Correlate the access log with the trace when one is active.
			if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
				fields["trace_id"] = sc.TraceID().String()
			}
			s.log.WithFields(fields).Info("request")
		}()

		if !isProbePath(r.URL.Path) && !s.authenticate(rec, r) {
			return
		}
		next.ServeHTTP(rec, r)
	})
}

// routePath retains the registered template, never a user-supplied path value.
func routePath(pattern string) string {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}

// isProbePath reports whether a path must stay reachable without an API key
// so orchestrators' liveness/readiness probes work.
func isProbePath(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// authenticate enforces the user API-key store when one is configured.
// Keys arrive as Authorization: Bearer or x-api-key. Anthropic clients get an
// Anthropic-shaped 401, OpenAI clients an OpenAI-shaped one.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if s.auth == nil {
		return true
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key == "" || key == "Bearer" {
		key = r.Header.Get("x-api-key")
	}
	user, ok := s.auth.Verify(key)
	if !ok {
		s.metrics.authFailures.Inc()
		message := "invalid API key"
		if key == "" {
			message = "missing API key"
		}
		writeError(w, r, http.StatusUnauthorized, "authentication_error", message)
		return false
	}
	s.metrics.authSuccesses.WithLabelValues(user).Inc()
	return true
}

// tracer is the global OTel tracer; it is a no-op unless OTEL_* environment
// variables installed a real provider at startup.
var tracer = otel.Tracer("llm-proxy")

type requestIDKey struct{}

// RequestID extracts the middleware-assigned request ID.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
