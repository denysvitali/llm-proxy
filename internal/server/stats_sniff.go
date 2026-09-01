package server

// Body sniffing and wire parsing: a bounded copy of each upstream body, and
// extraction of usage/tool-call statistics from the three wire dialects.

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Body sniffing

// sniffCap bounds how much of an upstream body is retained for parsing. The
// client always receives every byte; past the cap only the parsing copy
// stops growing (usage stats are lost for that response).
const sniffCap = 8 << 20

// sniffer forwards upstream bytes verbatim while keeping a bounded copy so
// usage/tool-call stats can be parsed at Finish.
type sniffer struct {
	body          io.ReadCloser
	tracker       *tracker
	sse           bool
	attemptStatus string // HTTP status of the attempt this body belongs to
	buf           bytes.Buffer
	dropped       bool
	closed        bool
}

// newSniffer wraps one attempt's body. The status is frozen at creation:
// several attempts may share a tracker across retries, and a deferred Finish
// must judge the body it held, not whatever status the tracker carries by
// the time Finish runs.
func newSniffer(body io.ReadCloser, tr *tracker, sse bool, status int) *sniffer {
	attemptStatus := statusError
	if status > 0 {
		attemptStatus = strconv.Itoa(status)
	}
	return &sniffer{body: body, tracker: tr, sse: sse, attemptStatus: attemptStatus}
}

func (sn *sniffer) Read(p []byte) (int, error) {
	n, err := sn.body.Read(p)
	if n > 0 {
		sn.tracker.noteFirstByte()
		if !sn.dropped {
			if int64(sn.buf.Len())+int64(n) > sniffCap {
				sn.dropped = true
				sn.buf.Reset()
			} else {
				sn.buf.Write(p[:n])
			}
		}
	}
	return n, err
}

// Close passes through; idempotent because relay helpers and forward paths
// may both defer it.
func (sn *sniffer) Close() error {
	if sn.closed {
		return nil
	}
	sn.closed = true
	return sn.body.Close()
}

// Finish parses whatever was retained and folds it into the tracker. Two
// post-processing rules apply to the retained body:
//
//   - An upstream reply that is not a 2xx records why: its body's error
//     message becomes the tracker's failure summary. Later successful attempts
//     leave earlier messages alone, so a recovered retry keeps the reason of
//     the attempt that failed.
//   - A 2xx reply whose JSON body carries a top-level "error" object (the
//     gateway/quota shape relayNativeBuffered rejects) marks the request as a
//     failure so uptime stays honest.
func (sn *sniffer) Finish() {
	data := sn.buf.Bytes()
	if len(data) == 0 {
		return
	}
	sn.tracker.rep.mergeMax(parseUsageReport(data, sn.sse))
	success := sn.attemptStatus != statusError && strings.HasPrefix(sn.attemptStatus, "2")
	if success && !sn.sse && errorShapedBody(data) != nil {
		// Gateway-200: the exchange will answer 502 for this. Mark the whole
		// request failed so uptime counts it, and keep the body's message.
		sn.tracker.bodyFail = true
		if msg := truncateMessage(upstreamErrorSummary(data)); msg != "" {
			sn.tracker.errMsg = msg
		}
		return
	}
	if !success {
		if msg := truncateMessage(upstreamErrorSummary(data)); msg != "" {
			sn.tracker.errMsg = msg
		}
	}
}

// ---------------------------------------------------------------------------
// Wire parsing

// usageReport is the stat-bearing content extracted from one upstream
// response (or folded over a stream).
type usageReport struct {
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
	toolCalls  int64
}

// mergeMax folds another report in, keeping the high-water mark per token
// field. Providers repeat growing cumulative values across stream events
// (Anthropic message_delta re-sends output totals), so max is both the right
// fold and idempotent for values that arrive once.
func (r *usageReport) mergeMax(o usageReport) {
	r.input = maxI(r.input, o.input)
	r.output = maxI(r.output, o.output)
	r.cacheRead = maxI(r.cacheRead, o.cacheRead)
	r.cacheWrite = maxI(r.cacheWrite, o.cacheWrite)
	r.toolCalls = maxI(r.toolCalls, o.toolCalls)
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// wireUsageFields unions the usage shapes of Anthropic Messages, OpenAI Chat
// Completions, and the OpenAI Responses API.
type wireUsageFields struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	PromptTokens             int64 `json:"prompt_tokens"`
	CompletionTokens         int64 `json:"completion_tokens"`
	PromptTokensDetails      *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// wireTyped is any {type: ...} discriminator: content blocks, Responses
// output items, streamed item announcements.
type wireTyped struct {
	Type string `json:"type"`
}

type wireToolRef struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
}

type wireDelta struct {
	ToolCalls []wireToolRef `json:"tool_calls"`
}

type wireChoice struct {
	Delta   *wireDelta `json:"delta"`   // streaming chunks
	Message *wireDelta `json:"message"` // non-streaming
}

// wireDoc unions the event/response envelopes that can carry usage or tool
// calls across the three dialects. Everything is optional; unknown shapes
// contribute nothing.
type wireDoc struct {
	Type    string           `json:"type"`
	Usage   *wireUsageFields `json:"usage"` // Anthropic root, chat chunk, Responses root
	Message *struct {
		Usage *wireUsageFields `json:"usage"` // Anthropic message_start
	} `json:"message"`
	Response *struct {
		Usage  *wireUsageFields `json:"usage"`
		Output []wireTyped      `json:"output"`
	} `json:"response"`
	Output       []wireTyped  `json:"output"` // Responses non-stream: items sit at the top level
	Choices      []wireChoice `json:"choices"`
	Content      []wireTyped  `json:"content"`       // Anthropic non-stream content blocks
	ContentBlock *wireTyped   `json:"content_block"` // Anthropic content_block_start
	Item         *wireTyped   `json:"item"`          // Responses response.output_item.added
}

// parseUsageReport extracts usage and tool-call counts from a full response
// body, splitting SSE frames when sse is true.
func parseUsageReport(data []byte, sse bool) usageReport {
	var rep usageReport
	seenTools := map[string]bool{}
	for _, doc := range iterWireDocs(data, sse) {
		var u *wireUsageFields
		switch {
		case doc.Usage != nil:
			u = doc.Usage
		case doc.Message != nil:
			u = doc.Message.Usage
		case doc.Response != nil:
			u = doc.Response.Usage
		}
		if u != nil {
			var part usageReport
			part.input = maxI(u.InputTokens, u.PromptTokens)
			part.output = maxI(u.OutputTokens, u.CompletionTokens)
			part.cacheRead = u.CacheReadInputTokens
			part.cacheWrite = u.CacheCreationInputTokens
			if u.PromptTokensDetails != nil {
				part.cacheRead = maxI(part.cacheRead, u.PromptTokensDetails.CachedTokens)
			}
			if u.InputTokensDetails != nil {
				part.cacheRead = maxI(part.cacheRead, u.InputTokensDetails.CachedTokens)
			}
			rep.mergeMax(part)
		}

		// Anthropic stream: each tool_use opens with a content_block_start.
		if doc.ContentBlock != nil && doc.ContentBlock.Type == "tool_use" {
			rep.toolCalls++
		}
		// Anthropic non-stream: tool_use blocks live in content.
		for _, b := range doc.Content {
			if b.Type == "tool_use" {
				rep.toolCalls++
			}
		}
		// Responses stream: each function call announces its output item.
		if doc.Item != nil && doc.Item.Type == "function_call" {
			rep.toolCalls++
		}
		// Responses non-stream: function_call items sit in the output array
		// (top level on the response object, nested in response.completed).
		outputs := doc.Output
		if doc.Response != nil {
			outputs = append(outputs, doc.Response.Output...)
		}
		if !sse {
			for _, item := range outputs {
				if item.Type == "function_call" {
					rep.toolCalls++
				}
			}
		}
		// OpenAI chat: stream deltas fragment tool calls — the index is the
		// stable identity across fragments; non-stream messages list them
		// complete, keyed by id.
		for _, ch := range doc.Choices {
			if ch.Delta != nil {
				for _, tc := range ch.Delta.ToolCalls {
					key := "idx:" + strconv.Itoa(tc.Index)
					if !seenTools[key] {
						seenTools[key] = true
						rep.toolCalls++
					}
				}
			}
			if ch.Message != nil {
				for _, tc := range ch.Message.ToolCalls {
					key := tc.ID
					if key == "" {
						key = "msg:" + strconv.Itoa(len(seenTools))
					}
					if !seenTools[key] {
						seenTools[key] = true
						rep.toolCalls++
					}
				}
			}
		}
	}
	return rep
}

// iterWireDocs yields each JSON document in the body: SSE data payloads when
// sse, otherwise the whole body as one document. Undecodable payloads are
// skipped silently — sniffing must never affect the proxied response.
func iterWireDocs(data []byte, sse bool) []wireDoc {
	if !sse {
		var doc wireDoc
		if err := json.Unmarshal(data, &doc); err != nil || !doc.hasSignal() {
			return nil
		}
		return []wireDoc{doc}
	}
	var docs []wireDoc
	for _, payload := range sseDataPayloads(data) {
		var doc wireDoc
		if json.Unmarshal(payload, &doc) == nil {
			docs = append(docs, doc)
		}
	}
	return docs
}

// hasSignal keeps bare documents (error bodies, unknown envelopes) out of the
// stats.
func (d *wireDoc) hasSignal() bool {
	return d.Usage != nil || d.Message != nil || d.Response != nil ||
		len(d.Choices) > 0 || len(d.Content) > 0 || d.ContentBlock != nil || d.Item != nil
}

// sseDataPayloads splits an SSE byte stream into the concatenated data lines
// of each event.
func sseDataPayloads(data []byte) [][]byte {
	var out [][]byte
	var cur bytes.Buffer
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, append([]byte(nil), bytes.TrimSpace(cur.Bytes())...))
			cur.Reset()
		}
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			flush()
			continue
		}
		if field, value, ok := bytes.Cut(line, []byte(":")); ok && string(field) == "data" {
			cur.Write(bytes.TrimPrefix(value, []byte(" ")))
			cur.WriteByte('\n')
		}
	}
	flush()
	return out
}
