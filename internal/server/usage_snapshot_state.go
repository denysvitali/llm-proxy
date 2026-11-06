package server

import "time"

// usageSnapshotState is the freshness of one cached account-usage snapshot
// (Grok usage, ZCode plans and quota, Open Design usage):
//
//	empty   --fetch ok--------------> fresh
//	fresh   --TTL elapses-----------> expired
//	expired --fetch ok--------------> fresh
//	fresh|expired --fetch fails-----> unchanged (the error is returned, the
//	                                  previous snapshot is kept)
//	fresh|expired --invalidate------> empty (ZCode claims)
//
// Only a fresh snapshot is served without fetching; Grok's forced refresh
// fetches even then.
type usageSnapshotState int

const (
	usageSnapshotEmpty usageSnapshotState = iota
	usageSnapshotFresh
	usageSnapshotExpired
)

func (s usageSnapshotState) String() string {
	switch s {
	case usageSnapshotEmpty:
		return "empty"
	case usageSnapshotFresh:
		return "fresh"
	case usageSnapshotExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// usageSnapshotStateAt classifies a snapshot fetched at fetchedAt. present is
// false when no snapshot exists or it was invalidated.
func usageSnapshotStateAt(present bool, fetchedAt time.Time, ttl time.Duration, now time.Time) usageSnapshotState {
	switch {
	case !present:
		return usageSnapshotEmpty
	case now.Sub(fetchedAt) < ttl:
		return usageSnapshotFresh
	default:
		return usageSnapshotExpired
	}
}
