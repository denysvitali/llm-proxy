package opendesign

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/denysvitali/llm-proxy/internal/sse"
)

// maxTokensCompleter repairs Open Design's Anthropic Messages streams that hit
// max_tokens. Upstream closes the last content block and then ends the
// stream cleanly, skipping the message_delta (stop_reason "max_tokens") and
// message_stop every other ending carries, so clients and the proxy saw a
// truncated stream instead of a length stop. Chat Completions and Responses
// report the limit correctly and are not wrapped.
//
// Bytes pass through unchanged. Only a clean EOF after message_start, with
// every content block closed and no message_delta, message_stop or error
// seen, gets the two missing events appended. Transport errors and streams
// cut inside a block stay broken, so real interruptions still surface.
type maxTokensCompleter struct {
	body      io.ReadCloser
	decoder   *sse.Decoder
	maxTokens int

	started    bool // message_start seen
	finished   bool // message_delta, message_stop or error seen
	openBlocks int
	closed     int // content blocks that completed

	tail []byte // synthesized events still to deliver
	eof  bool
}

func newMaxTokensCompleter(body io.ReadCloser, requestBody []byte) *maxTokensCompleter {
	var request struct {
		MaxTokens int `json:"max_tokens"`
	}
	_ = json.Unmarshal(requestBody, &request)
	c := &maxTokensCompleter{body: body, maxTokens: max(request.MaxTokens, 0)}
	c.decoder = sse.NewDecoder(func(event sse.Event) (bool, error) {
		c.observe(event)
		return false, nil
	})
	return c
}

func (c *maxTokensCompleter) observe(event sse.Event) {
	var payload struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(event.Data, &payload) != nil {
		return
	}
	switch payload.Type {
	case "message_start":
		c.started = true
	case "content_block_start":
		c.openBlocks++
	case "content_block_stop":
		if c.openBlocks > 0 {
			c.openBlocks--
			c.closed++
		}
	case "message_delta", "message_stop", "error":
		c.finished = true
	}
}

func (c *maxTokensCompleter) Read(p []byte) (int, error) {
	if c.eof {
		if len(c.tail) == 0 {
			return 0, io.EOF
		}
		n := copy(p, c.tail)
		c.tail = c.tail[n:]
		return n, nil
	}
	n, err := c.body.Read(p)
	if n > 0 {
		// A frame the decoder rejects (oversized) only disables the repair;
		// the bytes still reach the client.
		if _, feedErr := c.decoder.Feed(p[:n]); feedErr != nil {
			c.finished = true
		}
	}
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	c.eof = true
	if _, endErr := c.decoder.End(); endErr == nil && c.truncatedByLimit() {
		c.tail = []byte(fmt.Sprintf("\nevent: message_delta\ndata: "+
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":%d}}`+
			"\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", c.maxTokens))
	}
	if n > 0 || len(c.tail) > 0 {
		return n, nil
	}
	return 0, io.EOF
}

// truncatedByLimit reports the upstream's max_tokens ending: a started
// message whose blocks all closed and that then simply stopped.
func (c *maxTokensCompleter) truncatedByLimit() bool {
	return c.started && !c.finished && c.openBlocks == 0 && c.closed > 0
}

func (c *maxTokensCompleter) Close() error { return c.body.Close() }
