package translate

import (
	"encoding/json"
	"fmt"
)

// UpstreamError reports an upstream that answered an apparently successful
// HTTP status with a JSON error object instead of the wire format's success
// shape — fronting gateways and quota-limited providers do this, and without
// a check the body would reach clients disguised as a completed response.
type UpstreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    any    `json:"code,omitempty"`
}

// InvalidRequest identifies rejections that cannot be recovered by replaying
// the same input, including Responses context-window failures.
func (e *UpstreamError) InvalidRequest() bool {
	return e.Type == "invalid_request_error" || e.Code == "context_length_exceeded"
}

// ResponsesFailure extracts only the error from a Responses failure event;
// the surrounding response may contain prompts and other private content.
func ResponsesFailure(payload []byte) *UpstreamError {
	var event struct {
		Error    *UpstreamError `json:"error"`
		Response *struct {
			Error *UpstreamError `json:"error"`
		} `json:"response"`
		Message string `json:"message"`
		Code    any    `json:"code"`
	}
	_ = json.Unmarshal(payload, &event)
	err := event.Error
	if err == nil && event.Response != nil {
		err = event.Response.Error
	}
	if err == nil {
		err = &UpstreamError{Message: event.Message, Code: event.Code}
	}
	if err.Type == "" {
		err.Type = "api_error"
		if err.Code == "context_length_exceeded" {
			err.Type = "invalid_request_error"
		}
	}
	if err.Message == "" {
		err.Message = "upstream stream failed"
	}
	return err
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
	return &UpstreamError{Type: e.Type, Message: e.Message, Code: e.Code}
}
