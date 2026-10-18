package translate

// NormalizeResponsesToolArguments preserves argument bytes. Numeric spelling is
// part of a tool's payload, and a delta may end inside a string or number. Without
// the complete value and its schema, even an integer-valued float must not be
// coerced. This applies equally to deltas and terminal argument snapshots.
func NormalizeResponsesToolArguments(data []byte) []byte {
	return data
}
