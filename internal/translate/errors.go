package translate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
// the same input, including Responses context-window and policy failures.
func (e *UpstreamError) InvalidRequest() bool {
	return e.Type == "invalid_request_error" || e.Code == "context_length_exceeded" || e.Code == "cyber_policy"
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
	}
	if err.InvalidRequest() {
		err.Type = "invalid_request_error"
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

// itemLimitPattern matches the per-item history limit some gateways enforce on
// translated requests, e.g. Open Design's "conversation history exceeds the
// limit on input items (1976 > 1000 items)" behind code context_length_exceeded.
var itemLimitPattern = regexp.MustCompile(`exceeds the limit on input items \((\d+) > (\d+) items\)`)

// itemLimitGap reports the item counts of an item-limit rejection: (actual, limit, true).
func itemLimitGap(message string) (int, int, bool) {
	match := itemLimitPattern.FindStringSubmatch(message)
	if match == nil {
		return 0, 0, false
	}
	actual, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, false
	}
	limit, err := strconv.Atoi(match[2])
	if err != nil || limit <= 0 || actual <= limit {
		return 0, 0, false
	}
	return actual, limit, true
}

// AppendItemLimitGap makes an item-limit rejection actionable for Claude Code.
//
// Claude Code compacts by tokens: its recovery parses "prompt is too long:
// <actual> tokens > <limit> tokens" from the relayed error message and drops
// history until its own token estimate falls by the gap. An item-limit error
// names items instead, which its parser cannot read, so its compaction loop
// falls back to dropping 20% of the history per attempt and gives up after a
// few attempts — repeatedly re-sending still-oversized requests while the
// user watches the same error (observed: 1976 → 1052 items over six attempts,
// then the surfaced failure).
//
// When the upstream named an item overshoot and the caller supplies its token
// estimate of the request, the provider's message is kept verbatim and one
// parseable sentence is appended: the overshoot fraction, with margin, applied
// to the estimate. The proxy never rewrites history itself; it only tells the
// client how much to drop, in the one vocabulary it parses.
func AppendItemLimitGap(message string, estimatedTokens int) string {
	actual, limit, ok := itemLimitGap(message)
	if !ok || estimatedTokens <= 0 {
		return message
	}
	gap := estimatedTokens * (actual - limit) * 4 / (actual * 3) // ≈ 4/3 margin over the required fraction
	if rest := estimatedTokens * (actual - limit) / actual; gap < rest {
		gap = rest
	}
	if minimum := 4 * 1000; gap < minimum { // a drop too small to matter wastes the client's retry
		gap = minimum
	}
	kept, dropped := estimatedTokens, estimatedTokens-gap
	if dropped < 0 {
		dropped = 0
	}
	return message + " | prompt is too long: " + strconv.Itoa(kept) +
		" tokens > " + strconv.Itoa(dropped) + " tokens"
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
