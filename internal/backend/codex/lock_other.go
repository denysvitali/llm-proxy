//go:build !unix

package codex

import "context"

// lockFile is a no-op where flock is unavailable; only the in-process mutex
// serializes refreshes there, so run a single replica on such platforms.
func lockFile(context.Context, string) (func(), error) { return func() {}, nil }
