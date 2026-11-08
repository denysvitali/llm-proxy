// Package shape infers the key/type structure of JSON documents and SSE
// streams without retaining their values. It exists so the proxy can show
// what its translation matrix actually did to a request or response — which
// keys exist on each side, what type each carries, and which fields were
// added, removed, or retyped — without storing prompts, tool inputs, or
// credentials.
//
// Shapes are deliberately bounded: key maps, array samples, depth, event
// types, and the total node count are all capped, and a truncated region is
// marked with Cut instead of growing without limit. Inference never records a
// value, only a type, so a recorded shape cannot leak the content it
// describes.
package shape

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Node is one JSON value's inferred shape. Types is a sorted union because a
// single location can legitimately hold different types across array items or
// repeated documents (for example an Anthropic content block that is either a
// string or an object).
type Node struct {
	Types []string         `json:"types,omitempty"`
	Keys  map[string]*Node `json:"keys,omitempty"`
	Items *Node            `json:"items,omitempty"`
	// Cut marks a region dropped by a bound (depth, key count, array sample,
	// or node budget); its children are incomplete.
	Cut bool `json:"cut,omitempty"`
}

// JSON type names used in Node.Types.
const (
	TypeObject  = "object"
	TypeArray   = "array"
	TypeString  = "string"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	TypeNull    = "null"
)

// Bounds on inference. They are chosen so a complete shape stays small enough
// to embed in the 50-entry activity feed while still covering every API
// envelope the proxy forwards.
const (
	// MaxDepth is the deepest object/array nesting followed. Tool definition
	// schemas nest about six levels below the request root, so this leaves
	// headroom while stopping pathological payloads.
	MaxDepth = 10
	// maxKeys caps the keys recorded per object.
	maxKeys = 48
	// maxArraySamples caps how many array elements contribute to Items.
	maxArraySamples = 4
	// maxNodes caps the total nodes recorded for one document. Four shapes
	// (client/upstream request, upstream/client response) are retained per
	// attempt in a 50-entry ring, so this bound keeps the feed in the low
	// single-digit megabytes at worst.
	maxNodes = 512
	// maxEventTypes caps distinct SSE event names recorded per stream.
	maxEventTypes = 24
	// maxEventNameLength truncates provider-supplied event names.
	maxEventNameLength = 64
)

// Budget tracks the node allowance shared by one inference pass.
type Budget struct{ nodes int }

func (b *Budget) take() bool {
	if b.nodes >= maxNodes {
		return false
	}
	b.nodes++
	return true
}

// Infer returns the shape of a JSON document, or nil when data is not valid
// JSON. It accepts any top-level value; the proxy's request bodies are
// objects, but response bodies may be arrays.
func Infer(data []byte) *Node {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	node, err := inferValue(dec, 0, &Budget{})
	if err != nil {
		return nil
	}
	return node
}

func inferValue(dec *json.Decoder, depth int, budget *Budget) (*Node, error) {
	if depth > MaxDepth || !budget.take() {
		if err := skipValue(dec); err != nil {
			return nil, err
		}
		return &Node{Cut: true}, nil
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return inferObject(dec, depth, budget)
		case '[':
			return inferArray(dec, depth, budget)
		}
		return nil, nil
	case string:
		return &Node{Types: []string{TypeString}}, nil
	case json.Number, float64:
		return &Node{Types: []string{TypeNumber}}, nil
	case bool:
		return &Node{Types: []string{TypeBoolean}}, nil
	case nil:
		return &Node{Types: []string{TypeNull}}, nil
	}
	return nil, nil
}

func inferObject(dec *json.Decoder, depth int, budget *Budget) (*Node, error) {
	node := &Node{Types: []string{TypeObject}, Keys: map[string]*Node{}}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		child, err := inferValue(dec, depth+1, budget)
		if err != nil {
			return nil, err
		}
		if child == nil {
			continue
		}
		if _, exists := node.Keys[key]; !exists && len(node.Keys) >= maxKeys {
			node.Cut = true
			continue
		}
		node.Keys[key] = Merge(node.Keys[key], child)
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, err
	}
	return node, nil
}

func inferArray(dec *json.Decoder, depth int, budget *Budget) (*Node, error) {
	node := &Node{Types: []string{TypeArray}}
	index := 0
	for dec.More() {
		if index >= maxArraySamples {
			// Stop following elements; further elements cannot add a shape the
			// sample does not already represent often enough to be worth it.
			node.Cut = true
			if err := skipValue(dec); err != nil {
				return nil, err
			}
			index++
			continue
		}
		child, err := inferValue(dec, depth+1, budget)
		if err != nil {
			return nil, err
		}
		node.Items = Merge(node.Items, child)
		index++
	}
	if _, err := dec.Token(); err != nil { // closing bracket
		return nil, err
	}
	return node, nil
}

// skipValue consumes one JSON value from the token stream.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
				continue
			case '}', ']':
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

// Merge folds two shapes for the same location into one: types union, object
// keys merge recursively, array items merge recursively. Either argument may
// be nil.
func Merge(a, b *Node) *Node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := &Node{Cut: a.Cut || b.Cut}
	out.Types = unionSorted(a.Types, b.Types)
	out.Items = Merge(a.Items, b.Items)
	if a.Keys != nil || b.Keys != nil {
		out.Keys = make(map[string]*Node, len(a.Keys)+len(b.Keys))
		for key, node := range a.Keys {
			out.Keys[key] = node
		}
		for key, node := range b.Keys {
			out.Keys[key] = Merge(out.Keys[key], node)
		}
	}
	return out
}

func unionSorted(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	seen := make(map[string]bool, len(a)+len(b))
	for _, value := range a {
		seen[value] = true
	}
	for _, value := range b {
		seen[value] = true
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// Body is the inferred shape of one response body: a single JSON document or
// an SSE stream grouped by event name.
type Body struct {
	// Format is "json", "sse", "empty", or "unparsed".
	Format string `json:"format"`
	// Shape is set for Format "json".
	Shape *Node `json:"shape,omitempty"`
	// Events maps each SSE event name to the merged shape of its payloads.
	Events map[string]*Node `json:"events,omitempty"`
	// Order lists event names in first-seen order.
	Order []string `json:"order,omitempty"`
	// Skipped counts SSE payloads that were not JSON (for example "[DONE]").
	Skipped int `json:"skipped,omitempty"`
	// Truncated marks a stream that hit the event-type cap.
	Truncated bool `json:"truncated,omitempty"`
}

// FromBytes infers the shape of a complete response body, choosing between a
// JSON document and an SSE stream by the first non-space byte. Undecodable
// content yields a Body with Format "unparsed" rather than an error: shape
// recording must never affect delivery.
//
// SSE inference receives the untrimmed bytes: a trailing blank line is the
// frame terminator that makes the final event dispatchable, so trimming it
// would silently drop the stream's last event.
func FromBytes(data []byte) *Body {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return &Body{Format: "empty"}
	}
	switch trimmed[0] {
	case '{', '[':
		if node := Infer(trimmed); node != nil {
			return &Body{Format: "json", Shape: node}
		}
		return &Body{Format: "unparsed"}
	}
	stream := NewStream()
	_, _ = stream.Write(data)
	return stream.Body()
}

// bodyAccumulator merges SSE payload shapes by event name. It is shared by
// complete-body inference and by the relay-time Stream.
type bodyAccumulator struct {
	events  map[string]*Node
	order   []string
	skipped int
	frames  int
}

func (a *bodyAccumulator) add(payload []byte) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return
	}
	if payload[0] != '{' {
		a.skipped++
		return
	}
	var probe struct {
		Type   string `json:"type"`
		Object string `json:"object"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		a.skipped++
		return
	}
	name := probe.Type
	if name == "" {
		name = probe.Object
	}
	if name == "" {
		name = "data"
	}
	if len(name) > maxEventNameLength {
		name = name[:maxEventNameLength]
	}
	if a.events == nil {
		a.events = map[string]*Node{}
	}
	if _, known := a.events[name]; !known {
		if len(a.events) >= maxEventTypes {
			return
		}
		a.order = append(a.order, name)
	}
	node := Infer(payload)
	if node == nil {
		a.skipped++
		return
	}
	a.events[name] = Merge(a.events[name], node)
}

// body renders the accumulated payloads.
func (a *bodyAccumulator) body() *Body {
	if a.frames == 0 && a.skipped == 0 {
		return &Body{Format: "unparsed"}
	}
	out := &Body{Format: "sse", Skipped: a.skipped, Order: a.order, Events: a.events}
	if len(a.events) == 0 {
		out.Order, out.Events = nil, nil
	}
	return out
}

// MergeBodies folds two recorded bodies for the same response location. It is
// used when a retried attempt produced another body: identical shapes merge
// idempotently, and a meaningful body wins over an empty, unparsed, or
// truncated one.
func MergeBodies(a, b *Body) *Body {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Format == b.Format {
		switch a.Format {
		case "json":
			return &Body{Format: "json", Shape: Merge(a.Shape, b.Shape)}
		case "sse":
			out := &Body{Format: "sse", Skipped: a.Skipped + b.Skipped, Order: append([]string(nil), a.Order...)}
			out.Events = make(map[string]*Node, len(a.Events)+len(b.Events))
			for name, node := range a.Events {
				out.Events[name] = node
			}
			for name, node := range b.Events {
				if _, known := out.Events[name]; !known {
					out.Order = append(out.Order, name)
				}
				out.Events[name] = Merge(out.Events[name], node)
			}
			return out
		}
		return a
	}
	if bodyHasShape(a) {
		return a
	}
	return b
}

// bodyHasShape reports whether a body carries usable structure.
func bodyHasShape(b *Body) bool {
	switch b.Format {
	case "json":
		return b.Shape != nil
	case "sse":
		return len(b.Events) > 0
	}
	return false
}
