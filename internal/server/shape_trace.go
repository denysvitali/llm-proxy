package server

import (
	"time"

	"github.com/denysvitali/llm-proxy/internal/shape"
)

// ShapeTrace is the key/type structure of one upstream attempt on both sides
// of the translation matrix. It answers "what flows where" without retaining
// any value: the request the client sent, the request actually forwarded, the
// response the upstream returned, and the response relayed back — plus the
// structural diff at each hop.
//
// Only keys and JSON types are recorded. Prompts, tool arguments, and
// credentials cannot appear in a trace because inference never keeps a value.
type ShapeTrace struct {
	// ClientKind is the inbound API the client spoke; WireKind is the format
	// actually sent upstream. Together they name both ends of the conversion.
	ClientKind       string            `json:"client_kind,omitempty"`
	WireKind         string            `json:"wire_kind,omitempty"`
	RequestClient    *shape.Node       `json:"request_client,omitempty"`
	RequestUpstream  *shape.Node       `json:"request_upstream,omitempty"`
	RequestChanges   *shape.DiffReport `json:"request_changes,omitempty"`
	ResponseUpstream *shape.Body       `json:"response_upstream,omitempty"`
	ResponseClient   *shape.Body       `json:"response_client,omitempty"`
	ResponseChanges  *shape.DiffReport `json:"response_changes,omitempty"`

	// native marks a passthrough exchange, where the relayed bytes are the
	// upstream body itself; the response shape then needs no second tap.
	native bool
}

// hasContent reports whether any side of the trace was recorded.
func (t *ShapeTrace) hasContent() bool {
	return t != nil && (t.RequestClient != nil || t.RequestUpstream != nil ||
		bodyRecorded(t.ResponseUpstream) || bodyRecorded(t.ResponseClient))
}

// bodyRecorded reports whether a body carries a recorded format.
func bodyRecorded(body *shape.Body) bool {
	return body != nil && body.Format != "" && body.Format != "empty"
}

// noteClientRequest records the inbound request's shape.
func (t *tracker) noteClientRequest(node *shape.Node) {
	if node != nil {
		t.shapes.RequestClient = node
	}
}

// noteUpstreamRequest records the request shape as forwarded on the selected
// wire format. It is called once per attempt; the last attempt's payload is
// the one that produced the recorded response.
func (t *tracker) noteUpstreamRequest(node *shape.Node) {
	if node != nil {
		t.shapes.RequestUpstream = node
	}
}

// noteUpstreamResponse records the upstream body's shape. The last attempt
// wins: a retried stream restarts from the beginning, so the final body is
// the one that describes what actually happened, and an earlier failed
// attempt's error body must not contaminate it.
func (t *tracker) noteUpstreamResponse(body *shape.Body) {
	if bodyRecorded(body) {
		t.shapes.ResponseUpstream = body
	}
}

// noteClientResponse records what the client actually received. A retried
// stream starts over, so the last recorded body wins rather than merging
// duplicate events.
func (t *tracker) noteClientResponse(body *shape.Body) {
	if bodyRecorded(body) {
		t.shapes.ResponseClient = body
	}
}

// finishShapes computes the per-hop diffs and attaches the trace to the
// inspection record when anything was captured.
func (t *tracker) finishShapes() {
	trace := t.shapes
	if trace.RequestClient != nil && trace.RequestUpstream != nil {
		trace.RequestChanges = shape.Diff(jsonShape(trace.RequestClient), jsonShape(trace.RequestUpstream))
	}
	if trace.native && trace.ResponseClient == nil && bodyRecorded(trace.ResponseUpstream) {
		trace.ResponseClient = trace.ResponseUpstream
	}
	if trace.ResponseUpstream != nil && trace.ResponseClient != nil {
		trace.ResponseChanges = shape.Diff(trace.ResponseUpstream, trace.ResponseClient)
	}
	if trace.hasContent() {
		t.request.Shapes = &trace
	}
}

// jsonShape wraps an inferred request node as a JSON body so it can be diffed
// with the same machinery used for responses.
func jsonShape(node *shape.Node) *shape.Body {
	return &shape.Body{Format: "json", Shape: node}
}

// ShapeSummary is one retained attempt's conversion fingerprint: identity
// plus how much the proxy structurally changed on each hop. Full trees are
// available from the detail endpoint.
type ShapeSummary struct {
	ID             string      `json:"id"`
	At             time.Time   `json:"at"`
	ProxyRequestID string      `json:"proxy_request_id,omitempty"`
	Backend        string      `json:"backend"`
	Model          string      `json:"model"`
	Kind           string      `json:"kind,omitempty"`
	ClientKind     string      `json:"client_kind,omitempty"`
	Status         string      `json:"status"`
	Request        DiffSummary `json:"request_changes"`
	Response       DiffSummary `json:"response_changes"`
}

// DiffSummary is a diff report reduced to counts for feed-sized payloads.
type DiffSummary struct {
	Recorded bool `json:"recorded"`
	Changed  bool `json:"changed"`
	Added    int  `json:"added"`
	Removed  int  `json:"removed"`
	Retyped  int  `json:"retyped"`
	Cut      bool `json:"cut,omitempty"`
}

func diffSummary(report *shape.DiffReport) DiffSummary {
	if report == nil {
		return DiffSummary{}
	}
	return DiffSummary{
		Recorded: true,
		Changed:  !report.Identical || len(report.Changes) > 0,
		Added:    report.Added,
		Removed:  report.Removed,
		Retyped:  report.Retyped,
		Cut:      report.Cut,
	}
}

// shapeSummary renders one inspected request's fingerprint.
func shapeSummary(req InspectedRequest) ShapeSummary {
	summary := ShapeSummary{
		ID: req.ID, At: req.At, ProxyRequestID: req.ProxyRequestID,
		Backend: req.Backend, Model: req.Model, Kind: req.Kind, Status: req.Status,
	}
	if req.Shapes != nil {
		summary.ClientKind = req.Shapes.ClientKind
		summary.Request = diffSummary(req.Shapes.RequestChanges)
		summary.Response = diffSummary(req.Shapes.ResponseChanges)
	}
	return summary
}
