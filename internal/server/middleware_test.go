package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/prometheus/client_golang/prometheus/testutil"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func middlewareExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := tracer
	tracer = provider.Tracer("middleware-test")
	t.Cleanup(func() {
		tracer = previous
		_ = provider.Shutdown(context.Background())
	})
	return exporter
}

func TestMiddlewareIncomingTraceContext(t *testing.T) {
	exporter := middlewareExporter(t)
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid=%t", valid), func(t *testing.T) {
			exporter.Reset()
			log, hook := logtest.NewNullLogger()
			s := &Server{log: log, metrics: newMetrics()}
			var active trace.SpanContext
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
				active = trace.SpanContextFromContext(r.Context())
				if RequestID(r.Context()) == "" {
					t.Error("handler missing request ID")
				}
				if baggage.FromContext(r.Context()).Len() != 0 {
					t.Error("untrusted baggage was extracted")
				}
			})
			r := httptest.NewRequest(http.MethodGet, "/api/requests/arbitrary-id", nil)
			r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			r.Header.Set("tracestate", "vendor=value")
			r.Header.Set("baggage", "secret=untrusted")
			if !valid {
				r.Header.Set("traceparent", "invalid")
			}
			w := httptest.NewRecorder()
			s.withMiddleware(mux).ServeHTTP(w, r)
			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("spans = %d, want 1", len(spans))
			}
			span := spans[0]
			if span.SpanKind != trace.SpanKindServer || !active.Equal(span.SpanContext) {
				t.Errorf("handler/server span mismatch: active=%v span=%v", active, span)
			}
			if valid {
				if span.Parent.SpanID().String() != "00f067aa0ba902b7" || !span.Parent.IsRemote() ||
					span.SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" ||
					span.SpanContext.TraceState().String() != "vendor=value" {
					t.Errorf("remote trace context lost: %v", span)
				}
			} else if span.Parent.IsValid() {
				t.Errorf("invalid header created parent: %v", span.Parent)
			}
			entry := hook.LastEntry()
			if entry == nil || entry.Data["trace_id"] != span.SpanContext.TraceID().String() || entry.Data["status"] != http.StatusOK {
				t.Errorf("access log missing correlation/status: %v", entry)
			}
			if got := w.Header().Get("X-Request-Id"); got == "" || entry.Data["request_id"] != got {
				t.Errorf("request ID not correlated: header=%q log=%v", got, entry)
			}
		})
	}
}

func TestMiddlewareQuietRequestsKeepFailures(t *testing.T) {
	exporter := middlewareExporter(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		for _, outcome := range []string{"implicit success", "explicit success", "client failure", "server failure", "panic", "committed panic"} {
			t.Run(path+"/"+outcome, func(t *testing.T) {
				exporter.Reset()
				log, hook := logtest.NewNullLogger()
				s := &Server{log: log, metrics: newMetrics()}
				status := http.StatusOK
				mux := http.NewServeMux()
				var entered time.Time
				mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
					entered = time.Now()
					switch outcome {
					case "explicit success":
						w.WriteHeader(http.StatusNoContent)
						status = http.StatusNoContent
					case "client failure":
						status = http.StatusUnauthorized
						w.WriteHeader(status)
					case "server failure":
						status = http.StatusServiceUnavailable
						w.WriteHeader(status)
					case "panic":
						status = http.StatusInternalServerError
						panic("probe failed")
					case "committed panic":
						w.WriteHeader(http.StatusOK)
						panic("probe failed after headers")
					}
				})
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
				s.withMiddleware(mux).ServeHTTP(httptest.NewRecorder(), r)
				if got := testutil.ToFloat64(s.metrics.requests.WithLabelValues("GET", path, fmt.Sprint(status))); got != 1 {
					t.Errorf("request count = %v, want 1", got)
				}
				spans := exporter.GetSpans()
				if outcome == "implicit success" || outcome == "explicit success" {
					if len(spans) != 0 || len(hook.AllEntries()) != 0 {
						t.Errorf("successful maintenance request emitted spans/logs: %d/%d", len(spans), len(hook.AllEntries()))
					}
					return
				}
				if len(spans) != 1 || hook.LastEntry() == nil || hook.LastEntry().Message != "request" {
					t.Fatalf("failed maintenance request lost span/access log: %d/%v", len(spans), hook.AllEntries())
				}
				span := spans[0]
				if span.Parent.SpanID().String() != "00f067aa0ba902b7" || span.StartTime.After(entered) {
					t.Errorf("failure span lost parent or original start time: %v", span)
				}
				if outcome != "client failure" && span.Status.Code != codes.Error {
					t.Errorf("failure span status = %v, want Error", span.Status)
				}
				if outcome == "panic" || outcome == "committed panic" {
					if len(span.Events) == 0 {
						t.Error("panic span missing exception event")
					}
				}
			})
		}
	}
}

func TestMiddlewareMetricsUseBoundedRoutes(t *testing.T) {
	log, _ := logtest.NewNullLogger()
	s := &Server{log: log, metrics: newMetrics()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/requests/{id}", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /api/stats/backends/{backend}/{model}", func(w http.ResponseWriter, r *http.Request) {})
	h := s.withMiddleware(mux)
	for i := range 20 {
		for _, path := range []string{fmt.Sprintf("/api/requests/id-%d", i), fmt.Sprintf("/api/stats/backends/backend-%d/model-%d", i, i), fmt.Sprintf("/unknown-%d", i)} {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		}
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(fmt.Sprintf("CUSTOM%d", i), "/unknown", nil))
	}
	families, err := s.metrics.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "llm_proxy_requests_total" && family.GetName() != "llm_proxy_request_duration_seconds" {
			continue
		}
		if len(family.Metric) != 4 {
			t.Errorf("%s has %d series, want 4", family.GetName(), len(family.Metric))
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "path" {
					switch label.GetValue() {
					case "/api/requests/{id}", "/api/stats/backends/{backend}/{model}", "unmatched":
					default:
						t.Errorf("unexpected path label %q", label.GetValue())
					}
				}
			}
		}
	}
	// Authentication runs before ServeMux.ServeHTTP, so resolve its template
	// before auth as well rather than using the unauthenticated raw path.
	s.auth, err = auth.NewStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/requests/rejected-id", nil))
	if w.Code != http.StatusUnauthorized || testutil.ToFloat64(s.metrics.requests.WithLabelValues("GET", "/api/requests/{id}", "401")) != 1 {
		t.Error("auth rejection lost registered template")
	}
}

func TestMiddlewareTraceRouteIsTemplate(t *testing.T) {
	exporter := middlewareExporter(t)
	log, _ := logtest.NewNullLogger()
	s := &Server{log: log, metrics: newMetrics()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {})
	s.withMiddleware(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/arbitrary", nil))
	for _, attr := range exporter.GetSpans()[0].Attributes {
		if attr.Key == attribute.Key("http.route") && attr.Value.AsString() == "/{path...}" {
			return
		}
	}
	t.Error("trace missing catch-all route template")
}
