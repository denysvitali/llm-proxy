package translate

import (
	"bytes"
	"encoding/json"
)

// ToolReferenceText is what a tool_reference block becomes on routes that
// cannot carry it: the referenced tool is already declared in the request,
// so the reference only tells the model the tool is ready to call.
func ToolReferenceText(toolName string) string {
	return "Tool " + toolName + " is now available."
}

// InlineToolReferences rewrites every tool_reference block in a native
// Anthropic Messages body — inside tool_result content or directly in a
// message — into an equivalent text block. Claude Code's tool search answers
// with these references, and Anthropic-compatible upstreams that do not
// implement them can fail the whole request. Bodies without a reference are
// returned unchanged (same slice); the bool reports whether anything was
// rewritten. Fields other than the rewritten blocks keep their original
// bytes.
func InlineToolReferences(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(`"tool_reference"`)) {
		return body, false, nil
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, false, err
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return nil, false, err
	}
	changed := false
	for _, message := range messages {
		content, ok, err := inlineToolReferenceBlocks(message["content"], true)
		if err != nil {
			return nil, false, err
		}
		if ok {
			message["content"] = content
			changed = true
		}
	}
	if !changed {
		return body, false, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, false, err
	}
	request["messages"] = encoded
	out, err := json.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// inlineToolReferenceBlocks rewrites tool_reference blocks in one content
// array. String content and non-array values are left alone. descend lets a
// message's tool_result blocks have their own content rewritten too.
func inlineToolReferenceBlocks(raw json.RawMessage, descend bool) (json.RawMessage, bool, error) {
	if !bytes.Contains(raw, []byte(`"tool_reference"`)) {
		return raw, false, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// String content cannot hold a block; a quoted "tool_reference" in
		// plain text is just text.
		return raw, false, nil //nolint:nilerr // non-array content is valid and untouched
	}
	changed := false
	for i, block := range blocks {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		switch {
		case kind == "tool_reference":
			var name string
			_ = json.Unmarshal(block["tool_name"], &name)
			text, err := json.Marshal(ToolReferenceText(name))
			if err != nil {
				return nil, false, err
			}
			blocks[i] = map[string]json.RawMessage{
				"type": json.RawMessage(`"text"`),
				"text": text,
			}
			changed = true
		case kind == "tool_result" && descend:
			content, ok, err := inlineToolReferenceBlocks(block["content"], false)
			if err != nil {
				return nil, false, err
			}
			if ok {
				block["content"] = content
				changed = true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}
