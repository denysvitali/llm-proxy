package translate

import (
	"strings"
	"testing"
)

func TestNormalizeResponsesToolArguments(t *testing.T) {
	input := []byte(`{"type":"response.function_call_arguments.done","arguments":"{\"session_id\":63889.0,\"fraction\":1.5,\"label\":\"1.0\"}"}`)
	got := string(NormalizeResponsesToolArguments(input))
	if !strings.Contains(got, `63889`) || strings.Contains(got, `63889.0`) {
		t.Fatalf("integer-valued float was not normalized: %s", got)
	}
	if !strings.Contains(got, `1.5`) || !strings.Contains(got, `1.0`) {
		t.Fatalf("fractional or string value changed: %s", got)
	}
}

func TestNormalizeResponsesToolArgumentDelta(t *testing.T) {
	input := []byte(`{"type":"response.function_call_arguments.delta","delta":",\"n\":8000.0"}`)
	got := string(NormalizeResponsesToolArguments(input))
	if !strings.Contains(got, `8000`) || strings.Contains(got, `8000.0`) {
		t.Fatalf("argument delta was not normalized: %s", got)
	}
}

func TestNormalizeResponsesToolArgumentsLeavesTextAlone(t *testing.T) {
	input := []byte(`{"type":"response.output_text.delta","delta":"version 1.0"}`)
	if got := string(NormalizeResponsesToolArguments(input)); got != string(input) {
		t.Fatalf("text delta changed: %s", got)
	}
}
