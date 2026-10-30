package opencode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// toolNameRenamingReader wraps an SSE stream and renames OpenCode tool-call
// names in data lines back to the caller's original names.
type toolNameRenamingReader struct {
	src    *bufio.Reader
	closer io.Closer
	m      map[string]string
	buf    []byte
	eof    bool
}

func newToolNameRenamingReader(r io.Reader, m map[string]string) *toolNameRenamingReader {
	tr := &toolNameRenamingReader{src: bufio.NewReader(r), m: m}
	if c, ok := r.(io.Closer); ok {
		tr.closer = c
	}
	return tr
}

func (r *toolNameRenamingReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 && !r.eof {
		line, err := r.src.ReadString('\n')
		if err != nil && line == "" {
			r.eof = true
			if err == io.EOF {
				err = io.EOF
			}
			return 0, err
		}
		if err == io.EOF {
			r.eof = true
		}
		// Process data lines.
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "data:"))
			if payload != "" && payload != "[DONE]" {
				var obj map[string]any
				if json.Unmarshal([]byte(payload), &obj) == nil {
					renameToolCallsInChatChunk(obj, r.m)
					if b, err := json.Marshal(obj); err == nil {
						line = "data: " + string(b) + "\n"
					}
				}
			}
		}
		r.buf = append(r.buf, []byte(line)...)
	}
	if len(r.buf) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		return 0, nil
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	// With the buffer drained at EOF, the next Read reports io.EOF.
	return n, nil
}

func (r *toolNameRenamingReader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func renameToolCallsInChatChunk(obj map[string]any, m map[string]string) {
	if len(m) == 0 {
		return
	}
	choices, _ := obj["choices"].([]any)
	for _, c := range choices {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		delta, _ := cm["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		tcs, _ := delta["tool_calls"].([]any)
		for _, tc := range tcs {
			tcm, _ := tc.(map[string]any)
			if tcm == nil {
				continue
			}
			fn, _ := tcm["function"].(map[string]any)
			if fn == nil {
				continue
			}
			if name, _ := fn["name"].(string); name != "" {
				if orig, ok := m[name]; ok {
					fn["name"] = orig
				}
			}
		}
	}
}
