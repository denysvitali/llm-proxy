package translate

import "fmt"

// UpstreamError reports an upstream that answered an apparently successful
// HTTP status with a JSON error object instead of the wire format's success
// shape — fronting gateways and quota-limited providers do this, and without
// a check the body would reach clients disguised as a completed response.
type UpstreamError struct {
	Type    string // upstream error type, when the body carries one
	Message string
}

func (e *UpstreamError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("%s: %s", e.Type, e.Message)
	}
	return e.Message
}

// upstreamErrorObj is the {"error":{"message","type","code"}} payload
// OpenAI-shaped services attach to failed responses.
type upstreamErrorObj struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// checkUpstreamError turns a decoded error object into an *UpstreamError, or
// nil when the body carried none.
func checkUpstreamError(e *upstreamErrorObj) error {
	if e == nil {
		return nil
	}
	return &UpstreamError{Type: e.Type, Message: e.Message}
}
