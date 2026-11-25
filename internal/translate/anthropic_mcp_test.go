package translate

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlattenMCPBlocksLeavesOtherBodiesUntouched(t *testing.T) {
	body := []byte(`{"model":"m",  "messages":[{"role":"user","content":"mcp_tool_use"}]}`)
	got, changed := FlattenMCPBlocks(body)
	if changed || !bytes.Equal(got, body) {
		t.Fatalf("changed=%v body=%s", changed, got)
	}
}

func TestMCPBlockTextErrorAndStringContent(t *testing.T) {
	got := mcpBlockText([]byte(`{"type":"mcp_tool_result","tool_use_id":"m1","is_error":true,"content":"boom"}`))
	if got != "[MCP tool error id=m1]\nboom" {
		t.Fatalf("got %q", got)
	}
}

// Cross-wire routes used to reject MCP connector blocks outright.
func TestMCPBlocksTranslateToOpenAIChat(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"mcp_tool_use","id":"m1","name":"search","server_name":"docs","input":{}},{"type":"mcp_tool_result","tool_use_id":"m1","content":[{"type":"text","text":"found"}]}]},{"role":"user","content":"thanks"}]}`)
	request, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ToOpenAI(request, "up")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `[MCP tool call docs/search id=m1]`) || !strings.Contains(string(out), `found`) {
		t.Fatalf("translated = %s", out)
	}
}
