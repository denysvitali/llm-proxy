package grok

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNamespaceRestorationPreservesNumericLexemes(t *testing.T) {
	input := []byte(`{"sequence_number":9007199254740993,"fraction":1.00,"exponent":1e3,"item":{"type":"function_call","name":"tools.lookup","arguments":"{\"label\":\"release 1.0\",\"count\":9007199254740993}"}}`)
	got := restoreNamespaceJSON(input, []namespaceTool{{qualified: "tools.lookup", namespace: "tools", name: "lookup"}})
	var result struct {
		Sequence json.RawMessage `json:"sequence_number"`
		Fraction json.RawMessage `json:"fraction"`
		Exponent json.RawMessage `json:"exponent"`
		Item     struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Arguments string `json:"arguments"`
		} `json:"item"`
	}
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Sequence) != "9007199254740993" || string(result.Fraction) != "1.00" || string(result.Exponent) != "1e3" {
		t.Fatalf("numeric lexemes changed: %s", got)
	}
	if result.Item.Name != "lookup" || result.Item.Namespace != "tools" || result.Item.Arguments != `{"label":"release 1.0","count":9007199254740993}` {
		t.Fatalf("incorrect restoration: %s", got)
	}
	if unchanged := restoreNamespaceJSON(input, nil); !bytes.Equal(input, unchanged) {
		t.Fatalf("no-op changed bytes: %s", unchanged)
	}
}
