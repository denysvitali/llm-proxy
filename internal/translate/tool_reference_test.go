package translate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInlineToolReferences(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		want    string
		changed bool
	}{
		{
			name: "no references keeps bytes",
			body: `{"model":"m",  "messages":[{"role":"user","content":"hi"}]}`,
			want: `{"model":"m",  "messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "plain text mentioning tool_reference",
			body: `{"model":"m","messages":[{"role":"user","content":"what is a \"tool_reference\"?"}]}`,
			want: `{"model":"m","messages":[{"role":"user","content":"what is a \"tool_reference\"?"}]}`,
		},
		{
			name: "tool search result",
			body: `{"model":"m","tools":[{"name":"Weather","defer_loading":true}],"messages":[` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"ToolSearch","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":[` +
				`{"type":"tool_reference","tool_name":"Weather"},{"type":"text","text":"x"}]},` +
				`{"type":"text","text":"go","cache_control":{"type":"ephemeral"}}]}]}`,
			want: `{"model":"m","tools":[{"name":"Weather","defer_loading":true}],"messages":[` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"ToolSearch","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":[` +
				`{"type":"text","text":"Tool Weather is now available."},{"type":"text","text":"x"}]},` +
				`{"type":"text","text":"go","cache_control":{"type":"ephemeral"}}]}]}`,
			changed: true,
		},
		{
			name:    "top-level reference",
			body:    `{"messages":[{"role":"user","content":[{"type":"tool_reference","tool_name":"Bash"}]}]}`,
			want:    `{"messages":[{"role":"user","content":[{"type":"text","text":"Tool Bash is now available."}]}]}`,
			changed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, changed, err := InlineToolReferences([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if changed != test.changed {
				t.Fatalf("changed = %v, want %v", changed, test.changed)
			}
			if !test.changed {
				if string(got) != test.body {
					t.Fatalf("unchanged body rewritten: %s", got)
				}
				return
			}
			var gotValue, wantValue any
			if err := json.Unmarshal(got, &gotValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.want), &wantValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("got %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestInlineToolReferencesInvalidJSON(t *testing.T) {
	if _, _, err := InlineToolReferences([]byte(`{"messages":"tool_reference"`)); err == nil {
		t.Fatal("want error for malformed body")
	}
}
