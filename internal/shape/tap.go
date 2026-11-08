package shape

import "bytes"

// maxTapBodyBytes bounds the JSON document a client-response Tap buffers. SSE
// streams are decoded incrementally and need no buffer. A larger JSON body is
// reported as truncated rather than retained.
const maxTapBodyBytes = 8 << 20

// Tap accumulates the shape of bytes relayed to a client without changing
// them. It chooses between JSON and SSE by the first non-space byte: a JSON
// body is buffered up to maxTapBodyBytes and inferred when done, an SSE stream
// is decoded incrementally. Write never returns an error, so a Tap can sit in
// the relay path without affecting delivery.
type Tap struct {
	mode   tapMode
	prefix []byte // undecided leading bytes (whitespace plus the deciding byte)
	json   []byte
	sse    *Stream
}

type tapMode int

const (
	tapUndecided tapMode = iota
	tapJSON
	tapSSE
	tapTruncated
)

// NewTap returns a Tap ready to consume relayed bytes.
func NewTap() *Tap { return &Tap{} }

// Write records the shape of p and never fails.
func (t *Tap) Write(p []byte) (int, error) {
	if t.mode == tapTruncated {
		return len(p), nil
	}
	if t.mode == tapUndecided {
		t.prefix = append(t.prefix, p...)
		switch index := firstNonSpace(t.prefix); {
		case index < 0:
			// Still only whitespace; keep collecting. A pathological prefix is
			// treated as truncated rather than buffered forever.
			if len(t.prefix) > maxTapBodyBytes {
				t.mode = tapTruncated
				t.prefix = nil
			}
			return len(p), nil
		case t.prefix[index] == '{' || t.prefix[index] == '[':
			t.mode = tapJSON
			t.json = append(t.json, t.prefix...)
			t.prefix = nil
		default:
			t.mode = tapSSE
			t.sse = NewStream()
			_, _ = t.sse.Write(t.prefix)
			t.prefix = nil
		}
		return len(p), nil
	}
	if t.mode == tapSSE {
		_, _ = t.sse.Write(p)
		return len(p), nil
	}
	if len(t.json)+len(p) > maxTapBodyBytes {
		t.mode = tapTruncated
		t.json = nil
		return len(p), nil
	}
	t.json = append(t.json, p...)
	return len(p), nil
}

// Body finalizes and returns the recorded shape.
func (t *Tap) Body() *Body {
	switch t.mode {
	case tapSSE:
		return t.sse.Body()
	case tapJSON:
		if node := Infer(t.json); node != nil {
			return &Body{Format: "json", Shape: node}
		}
		return &Body{Format: "unparsed"}
	case tapTruncated:
		return &Body{Format: "truncated"}
	}
	return &Body{Format: "empty"}
}

func firstNonSpace(data []byte) int {
	for index, b := range data {
		if !bytes.ContainsRune([]byte(" \t\r\n"), rune(b)) {
			return index
		}
	}
	return -1
}
