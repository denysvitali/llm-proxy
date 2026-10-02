package upstream_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend/nous"
	"github.com/denysvitali/llm-proxy/internal/backend/opencodego"
	"github.com/denysvitali/llm-proxy/internal/backend/openrouter"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func TestSendTransfersBodyOwnershipIncludingHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader("data: live response\n\n")}
			header := http.Header{"Retry-After": {"10"}}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Accept") != "text/event-stream" || req.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request headers = %v", req.Header)
				}
				if req.Method != http.MethodPost {
					t.Errorf("request method = %q", req.Method)
				}
				return &http.Response{StatusCode: status, Header: header, Body: body}, nil
			})}
			req, err := upstream.JSONRequest(context.Background(), "https://upstream.invalid/chat", []byte(`{"model":"test"}`), true)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := upstream.Send(client, req, "Test Provider")
			if err != nil {
				t.Fatal(err)
			}
			if resp.Status != status || resp.Body != body || body.closed || body.reads != 0 {
				t.Fatalf("Send must retain status and leave body untouched: status=%d body=%#v", resp.Status, body)
			}
			header.Set("Retry-After", "20")
			if resp.Header.Get("Retry-After") != "10" {
				t.Fatal("response headers alias the upstream response")
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil || string(data) != "data: live response\n\n" {
				t.Fatalf("response body = %q, %v", data, err)
			}
			if err := resp.Body.Close(); err != nil || !body.closed {
				t.Fatalf("caller could not close response body: %v", err)
			}
		})
	}
}

func TestReadCatalogBoundsBodyAndPreservesHTTPFailure(t *testing.T) {
	const limit = 16 << 20
	body := &trackedBody{Reader: strings.NewReader(strings.Repeat("x", limit+1))}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer catalog-key" {
			t.Errorf("catalog request method=%q headers=%v", req.Method, req.Header)
		}
		if req.Header.Get("User-Agent") != "custom-agent" {
			t.Error("catalog request lost provider headers")
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"5"}}, Body: body}, nil
	})}
	req, err := upstream.CatalogRequest(context.Background(), "https://upstream.invalid/models", "catalog-key")
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "custom-agent")
	resp, err := upstream.ReadCatalog(client, req, "Test Provider")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "5" || len(resp.Body) != limit || !body.closed {
		t.Fatalf("catalog status=%d length=%d closed=%v headers=%v", resp.Status, len(resp.Body), body.closed, resp.Header)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadCatalogClosesBodyOnReadFailure(t *testing.T) {
	wantErr := errors.New("interrupted catalog body")
	body := &trackedBody{Reader: failingReader{err: wantErr}}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "" {
			t.Error("public catalog request added authentication")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}
	req, err := upstream.CatalogRequest(context.Background(), "https://upstream.invalid/models", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = upstream.ReadCatalog(client, req, "Test Provider")
	if !errors.Is(err, wantErr) || !body.closed {
		t.Fatalf("read failure = %v, body closed=%v", err, body.closed)
	}
}

func TestTransportErrorsRetainCancellationAndProviderContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})}
	req, err := upstream.JSONRequest(ctx, "https://upstream.invalid/chat", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_, sendErr := upstream.Send(client, req, "Test Provider")
	_, catalogErr := upstream.ReadCatalog(client, req, "Test Provider")
	for _, err := range []error{sendErr, catalogErr} {
		if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "request to Test Provider failed: ") {
			t.Errorf("transport error = %v", err)
		}
	}
}

func TestProviderTimeoutPoliciesRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		name    string
		client  *http.Client
		timeout time.Duration
	}{
		{"nous", nous.New("", "").HTTP, 5 * time.Minute},
		{"opencode-go", opencodego.New("", "").HTTP, 10 * time.Minute},
		{"openrouter", openrouter.New("", "").HTTP, 15 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport, ok := test.client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport type = %T", test.client.Transport)
			}
			if transport.ResponseHeaderTimeout != test.timeout || test.client.Timeout != 0 {
				t.Errorf("header timeout=%v total timeout=%v", transport.ResponseHeaderTimeout, test.client.Timeout)
			}
		})
	}
}
