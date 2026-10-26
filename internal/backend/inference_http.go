package backend

import (
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/propagation"
)

// DoInference sends an inference request with optional W3C trace propagation.
// Only the resolved backend origin is trusted; credentials, catalog requests,
// account operations, and redirected origins do not grant propagation consent.
// The client and request remain safe to reuse concurrently.
func DoInference(client *http.Client, req *http.Request, baseURL string, propagate bool) (*http.Response, error) {
	trusted, _ := url.Parse(baseURL)
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copyClient := *client
	copyClient.Transport = inferenceTransport{transport, trusted, propagate}
	return copyClient.Do(req)
}

type inferenceTransport struct {
	transport http.RoundTripper
	trusted   *url.URL
	propagate bool
}

func (t inferenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Inject into a transport-local clone, so net/http never copies injected
	// headers from the initial request when following redirects. Strip again on
	// every hop, including headers supplied by a caller or redirect callback.
	req = req.Clone(req.Context())
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for name := range req.Header {
		if strings.EqualFold(name, "traceparent") || strings.EqualFold(name, "tracestate") || strings.EqualFold(name, "baggage") {
			delete(req.Header, name)
		}
	}
	if t.propagate && sameOrigin(req.URL, t.trusted) {
		(propagation.TraceContext{}).Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	}
	return t.transport.RoundTrip(req)
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.Hostname() == "" || b.Hostname() == "" {
		return false
	}
	if a.Scheme != "http" && a.Scheme != "https" {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && originPort(a) == originPort(b)
}

func originPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
