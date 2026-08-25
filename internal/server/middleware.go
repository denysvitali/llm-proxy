package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder captures the response status for logging and metrics.
type statusRecorder struct {
	delegate http.ResponseWriter
	status   int
	bytes    int64
}

func (r *statusRecorder) WriteHeader(code int) {
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
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))

		spanCtx, span := tracer.Start(r.Context(), "llm-proxy request",
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("llm_proxy.request_id", requestID),
			))
		r = r.WithContext(spanCtx)

		defer func() {
			if p := recover(); p != nil {
				s.log.WithField("request_id", requestID).
					Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, p)
				if rec.status == 0 {
					writeError(rec, r, http.StatusInternalServerError, "api_error", "internal proxy error")
				}
			}
			span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
			span.End()
			fields := map[string]any{
				"request_id": requestID,
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     rec.status,
				"bytes":      rec.bytes,
				"duration":   time.Since(start).String(),
			}
			// Correlate the access log with the trace when one is active.
			if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
				fields["trace_id"] = sc.TraceID().String()
			}
			s.metrics.observe(r.Method, r.URL.Path, rec.status, time.Since(start))
			s.log.WithFields(fields).Info("request")
		}()

		if !isProbePath(r.URL.Path) && !s.authenticate(rec, r) {
			return
		}
		next.ServeHTTP(rec, r)
	})
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
