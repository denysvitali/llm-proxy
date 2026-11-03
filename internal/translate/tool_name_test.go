package translate

import (
	"strings"
	"testing"
)

func TestCanonicalToolNameAliasesBuiltinsOnly(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"bash", "Bash"},
		{"BASH", "Bash"},
		{"grep", "Grep"},
		{"Read", "Read"},
		{"my_bash", "my_bash"},
		{"bash_extra", "bash_extra"},
		{"", ""},
	} {
		if got := canonicalToolName(tc.in); got != tc.want {
			t.Errorf("canonicalToolName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToolUseBlockAliasesLowercaseName(t *testing.T) {
	call := openAIToolCall{}
	call.ID = "call_1"
	call.Function.Name = "bash"
	call.Function.Arguments = "{}"
	block, err := toolUseBlock(call)
	if err != nil {
		t.Fatal(err)
	}
	if block["name"] != "Bash" {
		t.Fatalf("tool name = %v, want Bash", block["name"])
	}
	if !strings.HasPrefix(block["id"].(string), "toolu_") {
		t.Fatalf("id = %v", block["id"])
	}
}
