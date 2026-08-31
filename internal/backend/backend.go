// Package backend defines the interface every upstream provider implements
// and the shared request/response types that flow through the proxy.
package backend

import (
	"context"
	"net/http"
)

// Kind selects which inbound API shape the client used.
type Kind string

const (
	// KindAnthropic is the Anthropic Messages API (/v1/messages).
	KindAnthropic Kind = "anthropic"
	// KindOpenAIChat is the OpenAI Chat Completions API (/v1/chat/completions).
	KindOpenAIChat Kind = "openai-chat"
	// KindOpenAIResponses is the OpenAI Responses API (/v1/responses).
	KindOpenAIResponses Kind = "openai-responses"
)

// Request carries the decoded inbound request. RawBody keeps the exact bytes
// the client sent so passthrough backends can forward them unmodified, while
// Model holds the client-requested model name for routing decisions.
type Request struct {
	Kind      Kind
	Model     string
	RawBody   []byte
	Header    http.Header
	Streaming bool
}

// Response is what a backend hands back to the server layer: an upstream HTTP
// response whose body is streamed straight to the client (after any required
// translation).
type Response struct {
	Status int
	Header http.Header
	Body   interface {
		Read([]byte) (int, error)
		Close() error
	}
}

// Backend is one upstream provider. Implementations must be safe for
// concurrent use.
type Backend interface {
	// Name returns the backend identifier used in config and metrics
	// ("venice", "opencode", "opencode-go", "grok", "codex", "nous", "apodex").
	Name() string
	// Models lists model IDs this backend serves. Used by /v1/models and the
	// dashboard; backends with no catalog may return a static list.
	Models(ctx context.Context) ([]string, error)
	// Supports reports whether this backend accepts the given inbound API
	// shape natively (passthrough, no translation). The server translates
	// when Supports is false and a route exists.
	Supports(kind Kind) bool
	// Send forwards a request upstream and returns the raw upstream response.
	// The caller owns closing Response.Body. Implementations translate the
	// Request into their native wire format themselves.
	Send(ctx context.Context, req *Request) (*Response, error)
}

// ModelWireOverrider is an optional Backend refinement for providers whose
// native wire support varies by model. When implemented, the server consults
// SupportsModel instead of Supports to pick a wire format, so a backend can
// keep native passthrough for some models while forcing translation for
// others (e.g. an endpoint that serves one API shape reliably only for part
// of its catalog).
type ModelWireOverrider interface {
	// SupportsModel reports whether the backend accepts the given inbound API
	// shape natively for this specific model.
	SupportsModel(kind Kind, model string) bool
}

// RequestPreviewer optionally exposes the final body a backend will put on
// the wire. The admin request inspector uses it for backends that normalize
// the server-prepared payload inside Send.
type RequestPreviewer interface {
	PreviewRequest(req *Request) ([]byte, error)
}
