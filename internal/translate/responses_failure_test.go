package translate

import "testing"

func TestResponsesFailure(t *testing.T) {
	for _, tc := range []struct {
		name, payload, message, kind string
		invalid                      bool
	}{
		{"nested", `{"type":"response.failed","response":{"error":{"message":"too long","code":"context_length_exceeded"}}}`, "too long", "invalid_request_error", true},
		{"cyber policy nested missing type", `{"type":"response.failed","response":{"error":{"message":"blocked by policy","code":"cyber_policy"}}}`, "blocked by policy", "invalid_request_error", true},
		{"cyber policy nested invalid request type", `{"type":"response.failed","response":{"error":{"type":"invalid_request_error","message":"blocked by policy","code":"cyber_policy"}}}`, "blocked by policy", "invalid_request_error", true},
		{"cyber policy nested server error type", `{"type":"response.failed","response":{"error":{"type":"server_error","message":"blocked by policy","code":"cyber_policy"}}}`, "blocked by policy", "invalid_request_error", true},
		{"cyber policy top-level error", `{"error":{"message":"blocked by policy","code":"cyber_policy"}}`, "blocked by policy", "invalid_request_error", true},
		{"cyber policy flat", `{"type":"error","message":"blocked by policy","code":"cyber_policy"}`, "blocked by policy", "invalid_request_error", true},
		{"flat", `{"type":"error","message":"too long","code":"context_length_exceeded"}`, "too long", "invalid_request_error", true},
		{"unknown server error code", `{"error":{"type":"server_error","message":"unavailable","code":"upstream_unavailable"}}`, "unavailable", "server_error", false},
		{"numeric code", `{"error":{"type":"server_error","message":"unavailable","code":500}}`, "unavailable", "server_error", false},
		{"no error", `{"type":"response.failed","response":{"instructions":"private"}}`, "upstream stream failed", "api_error", false},
		{"malformed", `{`, "upstream stream failed", "api_error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ResponsesFailure([]byte(tc.payload))
			if err.Message != tc.message || err.Type != tc.kind || err.InvalidRequest() != tc.invalid {
				t.Fatalf("error=%+v invalid=%t", err, err.InvalidRequest())
			}
		})
	}
}
