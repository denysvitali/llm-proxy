package shape

import (
	"sort"
)

// Change is one structural difference between the two sides of a conversion.
// Path uses JSON Pointer segments ("" is the root, array elements are "[]"),
// so a value's content is never needed to locate it.
type Change struct {
	Path string   `json:"path"`
	Kind string   `json:"kind"` // "added", "removed", "retyped", or "format"
	From []string `json:"from,omitempty"`
	To   []string `json:"to,omitempty"`
}

// Change kinds.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeRetyped = "retyped"
	ChangeFormat  = "format"
)

// DiffReport is the result of comparing two bodies. Changes is bounded so a
// wild translation cannot bloat the retained trace; the counts always describe
// the full difference.
type DiffReport struct {
	Changes   []Change `json:"changes,omitempty"`
	Added     int      `json:"added"`
	Removed   int      `json:"removed"`
	Retyped   int      `json:"retyped"`
	Cut       bool     `json:"cut,omitempty"`
	Identical bool     `json:"identical,omitempty"`
}

// maxChanges caps the retained change list.
const maxChanges = 200

// Diff compares two recorded bodies. Equal shapes produce an empty report with
// Identical set. A format mismatch (json vs sse vs empty) is reported as a
// single "format" change.
func Diff(from, to *Body) *DiffReport {
	report := &DiffReport{}
	if from == nil || to == nil {
		return report
	}
	if from.Format != to.Format {
		report.Changes = append(report.Changes, Change{Path: "", Kind: ChangeFormat, From: []string{from.Format}, To: []string{to.Format}})
		return report
	}
	switch from.Format {
	case "json":
		diffNodes(report, "", from.Shape, to.Shape)
	case "sse":
		diffEvents(report, from, to)
	default:
		report.Identical = true
	}
	if len(report.Changes) == 0 {
		report.Identical = true
	}
	return report
}

func (r *DiffReport) add(change Change) {
	switch change.Kind {
	case ChangeAdded:
		r.Added++
	case ChangeRemoved:
		r.Removed++
	case ChangeRetyped:
		r.Retyped++
	}
	if len(r.Changes) >= maxChanges {
		r.Cut = true
		return
	}
	r.Changes = append(r.Changes, change)
}

func diffEvents(report *DiffReport, from, to *Body) {
	names := make(map[string]bool, len(from.Events)+len(to.Events))
	for name := range from.Events {
		names[name] = true
	}
	for name := range to.Events {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		path := "/events/" + pointerEscape(name)
		left, leftOK := from.Events[name]
		right, rightOK := to.Events[name]
		switch {
		case !leftOK:
			report.add(Change{Path: path, Kind: ChangeAdded, To: right.Types})
		case !rightOK:
			report.add(Change{Path: path, Kind: ChangeRemoved, From: left.Types})
		default:
			diffNodes(report, path, left, right)
		}
	}
}

func diffNodes(report *DiffReport, path string, from, to *Node) {
	switch {
	case from == nil && to == nil:
		return
	case from == nil:
		report.add(Change{Path: path, Kind: ChangeAdded, To: to.Types})
		return
	case to == nil:
		report.add(Change{Path: path, Kind: ChangeRemoved, From: from.Types})
		return
	}
	if !sameTypes(from.Types, to.Types) {
		report.add(Change{Path: path, Kind: ChangeRetyped, From: from.Types, To: to.Types})
	}
	diffNodeTrees(report, path, from, to)
}

// diffNodeTrees compares the children of two typed nodes without re-reporting
// their own type set; it is shared by the changed and unchanged paths so
// structure below a retyped location is still reported.
func diffNodeTrees(report *DiffReport, path string, from, to *Node) {
	if from.Items != nil || to.Items != nil {
		diffNodes(report, path+"/[]", from.Items, to.Items)
	}
	keys := make(map[string]bool, len(from.Keys)+len(to.Keys))
	for key := range from.Keys {
		keys[key] = true
	}
	for key := range to.Keys {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		diffNodes(report, path+"/"+pointerEscape(key), from.Keys[key], to.Keys[key])
	}
}

func sameTypes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pointerEscape escapes a key for a JSON Pointer segment (RFC 6901).
func pointerEscape(key string) string {
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, key[i])
		}
	}
	return string(out)
}
