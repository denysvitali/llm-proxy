package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

// Claude Code's deferred-tool flow: tool_use blocks carry caller, tool search
// answers with tool_reference blocks, and late tools arrive as tool_addition /
// tool_removal blocks in system messages. Shapes come from the Claude Code
// 2.1.314 client.
const claudeCodeToolChangesBody = `{
	"model":"m","max_tokens":256,
	"messages":[
		{"role":"user","content":"find a tool"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"ToolSearch","input":{"query":"web"},"caller":{"type":"direct"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"tool_reference","tool_name":"WebFetch"}]}]},
		{"role":"system","content":[
			{"type":"tool_addition","tool":{"type":"tool_definition","definition":{"name":"Late","description":"late tool","input_schema":{"type":"object","properties":{}}}}},
			{"type":"tool_addition","tool":{"type":"tool_reference","name":"WebFetch"}},
			{"type":"tool_removal","tool":{"type":"tool_reference","name":"Gone"}, "cache_control":{"type":"ephemeral"}}
		]},
		{"role":"system","content":[{"type":"text","text":"hook context"},{"type":"tool_addition","tool":{"type":"tool_reference","name":"WebFetch"}}]},
		{"role":"user","content":"go"}
	],
	"tools":[
		{"name":"ToolSearch","input_schema":{"type":"object"}},
		{"name":"WebFetch","input_schema":{"type":"object"},"defer_loading":true},
		{"name":"Gone","input_schema":{"type":"object"}}
	]
}`

func TestClaudeCodeToolChangesToOpenAIChat(t *testing.T) {
	req, err := ParseRequest([]byte(claudeCodeToolChangesBody))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ToOpenAI(req, "m")
	if err != nil {
		t.Fatal(err)
	}
	var got openAIRequest
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range got.Tools {
		names = append(names, tool.Function.Name)
	}
	if strings.Join(names, ",") != "ToolSearch,WebFetch,Late" {
		t.Fatalf("tools = %v, want ToolSearch,WebFetch,Late", names)
	}
	roles := make([]string, 0, len(got.Messages))
	for _, message := range got.Messages {
		roles = append(roles, message.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,tool,system,user" {
		t.Fatalf("roles = %v", roles)
	}
	if !strings.Contains(string(encoded), "Tool WebFetch is now available.") {
		t.Fatalf("tool_reference not rendered as text: %s", encoded)
	}
	if strings.Contains(string(encoded), "caller") || strings.Contains(string(encoded), "tool_addition") {
		t.Fatalf("client-only fields leaked: %s", encoded)
	}
}

func TestClaudeCodeToolChangesToResponses(t *testing.T) {
	req, err := ParseRequest([]byte(claudeCodeToolChangesBody))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ToResponses(req, "m")
	if err != nil {
		t.Fatal(err)
	}
	var got responsesRequest
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range got.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "ToolSearch,WebFetch,Late" {
		t.Fatalf("tools = %v, want ToolSearch,WebFetch,Late", names)
	}
	if !strings.Contains(string(got.Input), "Tool WebFetch is now available.") || !strings.Contains(string(got.Input), "hook context") {
		t.Fatalf("input = %s", got.Input)
	}
}

func TestClaudeCodeToolChangeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, block, want string
	}{
		{"unknown tool kind", `{"type":"tool_addition","tool":{"type":"mystery"}}`, "system message: tool_addition requires a tool_reference with a name or a tool_definition"},
		{"unknown field", `{"type":"tool_removal","tool":{"type":"tool_reference","name":"x"},"extra":1}`, `system message: tool_removal requires a tool and no unsupported fields (unknown field "extra")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseRequest([]byte(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"},{"role":"system","content":[` + tc.block + `]}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ToOpenAI(req, "m"); err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
