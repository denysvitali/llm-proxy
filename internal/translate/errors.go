package translate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

// openAIContextPattern matches OpenAI's numbered window rejection: "maximum
// context length is 128000 tokens. However, your messages resulted in 130000
// tokens" (or "you requested 130000 tokens").
var openAIContextPattern = regexp.MustCompile(`(?i)maximum context length is (\d+) tokens.*?(?:resulted in|requested) (\d+) tokens`)

// promptTooLongPattern is Claude Code's own parser for context overflows.
var promptTooLongPattern = regexp.MustCompile(`(?i)prompt is too long[^0-9]*\d+\s*tokens?\s*>\s*\d+`)

// itemLimitGap reports the item counts of an item-limit rejection: (actual, limit, true).
func itemLimitGap(message string) (int, int, bool) {
	return overshoot(itemLimitPattern.FindStringSubmatch(message), 1, 2)
}

// overshoot reads (actual, limit) from submatches at the given positions and
// reports whether they describe a real overshoot.
func overshoot(match []string, actualAt, limitAt int) (int, int, bool) {
	if match == nil {
		return 0, 0, false
	}
	actual, err := strconv.Atoi(match[actualAt])
	if err != nil {
		return 0, 0, false
	}
	limit, err := strconv.Atoi(match[limitAt])
	if err != nil || limit <= 0 || actual <= limit {
		return 0, 0, false
	}
	return actual, limit, true
}

// AppendContextLimitGap makes a context-overflow rejection actionable for
// Claude Code.
//
// Claude Code recovers from an overflow by parsing "prompt is too long:
// <actual> tokens > <limit> tokens" from the relayed error message and
// dropping history until its own token estimate falls by the gap. Other
// providers phrase the same rejection differently, which its parser cannot
// read, so its compaction loop falls back to dropping 20% of the history per
// attempt and gives up after a few attempts — repeatedly re-sending
// still-oversized requests while the user watches the same error (observed
// on Open Design: 1976 → 1052 items over six attempts, then the surfaced
// failure). Three phrasings are recognized:
//
//   - an item overshoot ("limit on input items (1052 > 1000 items)"): the
//     overshoot fraction, with a 4/3 margin, applied to the token estimate;
//   - OpenAI's numbered window ("maximum context length is N tokens … resulted
//     in M tokens"): the provider's own counts, verbatim;
//   - a bare context_length_exceeded without counts (Open Design's "exceeds
//     the model's token context limit"): an eighth of the estimate, enough to
//     clear the usual one-turn overshoot without discarding most of the
//     history.
//
// The provider's message is kept verbatim and one parseable sentence is
// appended; messages that already carry Claude Code's phrasing are left
// alone. estimatedTokens is the caller's rough estimate of the request; the
// proxy never rewrites history itself, it only tells the client how much to
// drop, in the one vocabulary it parses.
func AppendContextLimitGap(message string, estimatedTokens int) string {
	if promptTooLongPattern.MatchString(message) {
		return message
	}
	if actual, limit, ok := overshoot(openAIContextPattern.FindStringSubmatch(message), 2, 1); ok {
		return appendPromptTooLong(message, actual, limit)
	}
	if estimatedTokens <= 0 {
		return message
	}
	var gap int
	if actual, limit, ok := itemLimitGap(message); ok {
		gap = estimatedTokens * (actual - limit) * 4 / (actual * 3) // ≈ 4/3 margin over the required fraction
		if rest := estimatedTokens * (actual - limit) / actual; gap < rest {
			gap = rest
		}
	} else if strings.Contains(message, "context_length_exceeded") {
		gap = estimatedTokens / 8
	} else {
		return message
	}
	if minimum := 4 * 1000; gap < minimum { // a drop too small to matter wastes the client's retry
		gap = minimum
	}
	return appendPromptTooLong(message, estimatedTokens, max(estimatedTokens-gap, 0))
}

func appendPromptTooLong(message string, actual, limit int) string {
	return message + " | prompt is too long: " + strconv.Itoa(actual) +
		" tokens > " + strconv.Itoa(limit) + " tokens"
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
