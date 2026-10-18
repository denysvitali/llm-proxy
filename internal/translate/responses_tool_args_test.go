package translate

import (
	"encoding/json"
	"testing"
)

func TestNormalizeResponsesToolArgumentsPreservesWire(t *testing.T) {
	for _, input := range []string{
		`{"type":"response.function_call_arguments.done","arguments":"{\"session_id\":63889.0,\"fraction\":1.5,\"label\":\"1.0\"}"}`,
		`{"type":"response.function_call_arguments.delta","delta":",\"n\":8000.0"}`,
		`{"type":"response.output_text.delta","delta":"version 1.0"}`,
		`{"output":[{"type":"function_call","arguments":"{\"n\":9007199254740993}"}]}`,
	} {
		if got := string(NormalizeResponsesToolArguments([]byte(input))); got != input {
			t.Fatalf("payload changed: %s", got)
		}
	}
}

func TestArgumentFragmentsPreserveEverySplit(t *testing.T) {
	arguments := `{"label":"release 1.0 with \"quotes\" and \\slashes","count":9007199254740993,"fraction":1.05,"exponent":1.0e3}`
	if !json.Valid([]byte(arguments)) {
		t.Fatal("invalid argument fixture")
	}
	for split := 0; split <= len(arguments); split++ {
		var assembled string
		for _, fragment := range []string{arguments[:split], arguments[split:]} {
			payload, err := json.Marshal(map[string]string{"type": "response.function_call_arguments.delta", "delta": fragment})
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(NormalizeResponsesToolArguments(payload), &event); err != nil {
				t.Fatal(err)
			}
			assembled += event.Delta
		}
		if assembled != arguments {
			t.Fatalf("split %d changed arguments: %q", split, assembled)
		}
	}
}
