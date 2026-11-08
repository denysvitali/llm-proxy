package shape_test

import (
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/shape"
)

func TestInferRecordsKeysAndTypesWithoutValues(t *testing.T) {
	data := `{"model":"alias","stream":true,"max_tokens":1024,"temperature":0.2,` +
		`"stop":null,"messages":[{"role":"user","content":"secret prompt"}]}`
	node := shape.Infer([]byte(data))
	if node == nil {
		t.Fatal("Infer returned nil for valid JSON")
	}
	want := map[string][]string{
		"model": {"string"}, "stream": {"boolean"}, "max_tokens": {"number"},
		"temperature": {"number"}, "stop": {"null"},
	}
	for key, types := range want {
		child, ok := node.Keys[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
		if strings.Join(child.Types, ",") != strings.Join(types, ",") {
			t.Errorf("%s types = %v, want %v", key, child.Types, types)
		}
	}
	messages := node.Keys["messages"]
	if messages == nil || messages.Items == nil || messages.Items.Keys["role"] == nil {
		t.Fatalf("messages shape = %+v", messages)
	}
	// A shape must never carry the string itself.
	if encoded := encode(t, node); strings.Contains(encoded, "secret prompt") || strings.Contains(encoded, "alias") {
		t.Fatalf("shape leaked a value: %s", encoded)
	}
}

func TestInferUnionsArrayItemTypes(t *testing.T) {
	node := shape.Infer([]byte(`{"content":["text",{"type":"text"},42]}`))
	items := node.Keys["content"].Items
	if items == nil {
		t.Fatal("array items missing")
	}
	if got := strings.Join(items.Types, ","); got != "number,object,string" {
		t.Fatalf("item types = %q, want number,object,string", got)
	}
}

func TestInferMarksCuts(t *testing.T) {
	deep := strings.Repeat(`{"a":`, shape.MaxDepth+3) + "1" + strings.Repeat("}", shape.MaxDepth+3)
	node := shape.Infer([]byte(deep))
	if node == nil {
		t.Fatal("deep document was rejected")
	}
	var foundCut bool
	for current := node; current != nil; current = current.Keys["a"] {
		if current.Cut {
			foundCut = true
			break
		}
		if current.Keys == nil {
			break
		}
	}
	if !foundCut {
		t.Fatal("depth bound did not mark a cut")
	}

	many := `{"k":` + strings.Repeat("[", 2) + `1` + strings.Repeat("]", 2) + `}`
	if shape.Infer([]byte(many)) == nil {
		t.Fatal("nested array rejected")
	}

	long := `{"a":[` + strings.Repeat("1,", 20) + `1]}`
	arr := shape.Infer([]byte(long)).Keys["a"]
	if !arr.Cut {
		t.Fatal("array sample bound did not mark a cut")
	}
}

func TestInferRejectsInvalidJSON(t *testing.T) {
	for _, data := range []string{"", "{", "not json", `{"a":}`} {
		if node := shape.Infer([]byte(data)); node != nil {
			t.Errorf("Infer(%q) = %+v, want nil", data, node)
		}
	}
}

func TestFromBytesClassifiesBodies(t *testing.T) {
	if got := shape.FromBytes(nil); got.Format != "empty" {
		t.Errorf("empty body format = %q", got.Format)
	}
	if got := shape.FromBytes([]byte(`{"a":1}`)); got.Format != "json" || got.Shape == nil {
		t.Errorf("json body = %+v", got)
	}
	if got := shape.FromBytes([]byte(`[1,2]`)); got.Format != "json" {
		t.Errorf("array body = %+v", got)
	}
	if got := shape.FromBytes([]byte("plain text")); got.Format != "unparsed" {
		t.Errorf("text body = %+v", got)
	}
}

func TestFromBytesGroupsSSeEvents(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":3}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	body := shape.FromBytes([]byte(stream))
	if body.Format != "sse" {
		t.Fatalf("format = %q", body.Format)
	}
	if body.Skipped != 1 {
		t.Errorf("skipped = %d, want 1 ([DONE])", body.Skipped)
	}
	if got := strings.Join(body.Order, ","); got != "message_start,content_block_delta,message_stop" {
		t.Errorf("order = %q", got)
	}
	delta := body.Events["content_block_delta"]
	if delta == nil || delta.Keys["delta"] == nil || delta.Keys["delta"].Keys["text"] == nil {
		t.Fatalf("delta shape = %+v", delta)
	}
	if encoded := encode(t, body); strings.Contains(encoded, "hi there") {
		t.Fatalf("stream shape leaked text: %s", encoded)
	}
}

func TestStreamMatchesFromBytes(t *testing.T) {
	frames := "data: {\"type\":\"a\",\"x\":1}\n\ndata: {\"type\":\"b\",\"y\":\"z\"}\n\n"
	stream := shape.NewStream()
	for _, chunk := range chunked([]byte(frames), 7) {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	relayed := stream.Body()
	whole := shape.FromBytes([]byte(frames))
	if encode(t, relayed) != encode(t, whole) {
		t.Fatalf("chunked stream = %s, whole body = %s", encode(t, relayed), encode(t, whole))
	}
}

func TestDiffReportsAddedRemovedAndRetyped(t *testing.T) {
	from := shape.FromBytes([]byte(`{"model":"a","messages":[{"role":"user","content":"x"}],"stream":true,"max_tokens":10}`))
	to := shape.FromBytes([]byte(`{"model":"a","input":[{"role":"user","content":[{"type":"text","text":"x"}]}],"stream":"yes"}`))
	report := shape.Diff(from, to)
	if report.Identical {
		t.Fatal("diff reported identical shapes")
	}
	byPath := map[string]string{}
	for _, change := range report.Changes {
		byPath[change.Path] = change.Kind
	}
	want := map[string]string{
		"/messages":   shape.ChangeRemoved,
		"/max_tokens": shape.ChangeRemoved,
		"/input":      shape.ChangeAdded,
		"/stream":     shape.ChangeRetyped,
	}
	for path, kind := range want {
		if byPath[path] != kind {
			t.Errorf("%s = %q, want %q (all: %v)", path, byPath[path], kind, byPath)
		}
	}
	if report.Removed != 2 || report.Added != 1 || report.Retyped != 1 {
		t.Errorf("counts = added %d removed %d retyped %d", report.Added, report.Removed, report.Retyped)
	}
}

func TestDiffIdenticalShapes(t *testing.T) {
	body := shape.FromBytes([]byte(`{"a":{"b":[1,2]}}`))
	report := shape.Diff(body, body)
	if !report.Identical || len(report.Changes) != 0 {
		t.Fatalf("diff = %+v", report)
	}
}

func TestDiffFormatMismatch(t *testing.T) {
	jsonBody := shape.FromBytes([]byte(`{"a":1}`))
	sseBody := shape.FromBytes([]byte("data: {\"type\":\"x\"}\n\n"))
	report := shape.Diff(jsonBody, sseBody)
	if len(report.Changes) != 1 || report.Changes[0].Kind != shape.ChangeFormat {
		t.Fatalf("format diff = %+v", report)
	}
}

func TestDiffEvents(t *testing.T) {
	from := shape.FromBytes([]byte("data: {\"type\":\"a\",\"x\":1}\n\ndata: {\"type\":\"b\"}\n\n"))
	to := shape.FromBytes([]byte("data: {\"type\":\"a\",\"x\":\"1\"}\n\ndata: {\"type\":\"c\"}\n\n"))
	report := shape.Diff(from, to)
	byPath := map[string]string{}
	for _, change := range report.Changes {
		byPath[change.Path] = change.Kind
	}
	if byPath["/events/a/x"] != shape.ChangeRetyped {
		t.Errorf("retyped event field = %v", byPath)
	}
	if byPath["/events/b"] != shape.ChangeRemoved || byPath["/events/c"] != shape.ChangeAdded {
		t.Errorf("event add/remove = %v", byPath)
	}
}

func TestDiffEscapesPointerSegments(t *testing.T) {
	from := shape.FromBytes([]byte(`{"a/b~c":1}`))
	to := shape.FromBytes([]byte(`{}`))
	report := shape.Diff(from, to)
	if len(report.Changes) != 1 || report.Changes[0].Path != "/a~1b~0c" {
		t.Fatalf("pointer = %+v", report.Changes)
	}
}

func TestTapHandlesChunkedJSONAndTruncation(t *testing.T) {
	tap := shape.NewTap()
	body := "  {\"a\":1,\"b\":[true]}"
	for _, chunk := range chunked([]byte(body), 3) {
		if _, err := tap.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	got := tap.Body()
	if got.Format != "json" || got.Shape.Keys["a"] == nil || got.Shape.Keys["b"].Items.Types[0] != "boolean" {
		t.Fatalf("tap body = %s", encode(t, got))
	}
	if got := shape.NewTap().Body(); got.Format != "empty" {
		t.Errorf("empty tap = %+v", got)
	}
}

func TestTapStreamsMatchFromBytes(t *testing.T) {
	frames := "data: {\"type\":\"a\",\"x\":1}\n\ndata: {\"type\":\"b\"}\n\n"
	tap := shape.NewTap()
	for _, chunk := range chunked([]byte(frames), 5) {
		_, _ = tap.Write(chunk)
	}
	if encode(t, tap.Body()) != encode(t, shape.FromBytes([]byte(frames))) {
		t.Fatalf("tap = %s, whole = %s", encode(t, tap.Body()), encode(t, shape.FromBytes([]byte(frames))))
	}
}

func TestMergeBodies(t *testing.T) {
	first := shape.FromBytes([]byte("data: {\"type\":\"a\",\"x\":1}\n\n"))
	second := shape.FromBytes([]byte("data: {\"type\":\"b\",\"y\":2}\n\ndata: {\"type\":\"a\",\"x\":2}\n\n"))
	merged := shape.MergeBodies(first, second)
	if merged.Format != "sse" || len(merged.Events) != 2 || strings.Join(merged.Order, ",") != "a,b" {
		t.Fatalf("merged = %s", encode(t, merged))
	}
	if merged.Events["a"].Keys["x"].Types[0] != "number" {
		t.Fatalf("merged event = %s", encode(t, merged))
	}
	empty := &shape.Body{Format: "empty"}
	if got := shape.MergeBodies(empty, first); got != first {
		t.Errorf("empty did not defer: %s", encode(t, got))
	}
	if got := shape.MergeBodies(first, empty); got != first {
		t.Errorf("empty overwrote: %s", encode(t, got))
	}
	jsonA := shape.FromBytes([]byte(`{"a":1}`))
	jsonB := shape.FromBytes([]byte(`{"b":"x"}`))
	got := shape.MergeBodies(jsonA, jsonB)
	if len(got.Shape.Keys) != 2 {
		t.Fatalf("json merge = %s", encode(t, got))
	}
}
