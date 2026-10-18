package server

import "testing"

func TestRequestEnvelopeRoutingAgreement(t *testing.T) {
	for _, input := range []string{
		`{"model":"alias","stream":true,"nested":{"model":"other"}}`,
		`{"nested":[{"text":"escaped \\"},null,123],"mo\u0064el":"alias"}`,
		`{"model":"alias","large":9007199254740993,"text":"keep \\n bytes"}`,
	} {
		env, err := decodeOpenAIEnvelope([]byte(input))
		if err != nil {
			t.Fatalf("decode %s: %v", input, err)
		}
		rewritten, err := rewriteModel([]byte(input), "upstream")
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeOpenAIEnvelope(rewritten)
		if err != nil || got.Model != "upstream" || got.Stream != env.Stream {
			t.Fatalf("routing mismatch: %+v, %v", got, err)
		}
	}
}

func TestRequestEnvelopeRejectsAmbiguousRouting(t *testing.T) {
	for _, input := range []string{
		`{"model":"a","model":"b"}`,
		`{"Model":"a"}`, `{"model":"a","MODEL":"b"}`,
		`{"model":"a","mo\u0064el":"b"}`,
		`{"model":"a","stream":false,"stream":true}`,
		`{"model":"a","Stream":true}`,
		`{"model":"a","stream":null}`,
		`{"model":null}`, `{"model":123}`, `{"model":" "}`,
		`{}`, `null`, `[]`, `{"model":"a"} {}`,
	} {
		if _, err := decodeOpenAIEnvelope([]byte(input)); err == nil {
			t.Errorf("envelope accepted %s", input)
		}
		if _, err := rewriteModel([]byte(input), "upstream"); err == nil {
			t.Errorf("rewrite accepted %s", input)
		}
	}
}
