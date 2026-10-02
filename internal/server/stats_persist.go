package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func (st *Stats) startPersist() {
	st.stopCh = make(chan struct{})
	ticker := time.NewTicker(st.cfg.PersistInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				st.persist()
			case <-st.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Close stops the background persistence goroutine and flushes once.
func (st *Stats) Close() error {
	if st.stopCh != nil {
		st.stopOnce.Do(func() {
			close(st.stopCh)
		})
		st.persist()
	}
	if st.redis != nil {
		st.redis.close()
	}
	return nil
}

// statsSnapshot is the backwards-compatible versioned JSON storage format.
type statsSnapshot struct {
	Version int                      `json:"version"`
	SavedAt time.Time                `json:"saved_at"`
	Models  map[string]modelSnapshot `json:"models"`
}

type modelSnapshot struct {
	Requests   uint64            `json:"requests"`
	Successes  uint64            `json:"successes"`
	TokensIn   uint64            `json:"tokens_in"`
	TokensOut  uint64            `json:"tokens_out"`
	CacheRead  uint64            `json:"cache_read"`
	CacheWrite uint64            `json:"cache_write"`
	ToolCalls  uint64            `json:"tool_calls"`
	ToolErrors uint64            `json:"tool_errors"`
	Statuses   map[string]uint64 `json:"status_codes,omitempty"`
	Buckets    []bucket          `json:"buckets"`
}

// snapshotForPersist owns every map and slice it returns. Persistence can encode
// it after releasing the model locks while requests continue to update stats.
func (st *Stats) snapshotForPersist() *statsSnapshot {
	return &statsSnapshot{Version: 1, SavedAt: time.Now().UTC(), Models: st.snapshotModels()}
}

func (st *Stats) snapshotModels() map[string]modelSnapshot {
	models := make(map[string]modelSnapshot)
	st.mu.RLock()
	defer st.mu.RUnlock()
	for key, ms := range st.models {
		ms.mu.Lock()
		snapshot := modelSnapshot{
			Requests: ms.requests, Successes: ms.successes,
			TokensIn: ms.tokensIn, TokensOut: ms.tokensOut,
			CacheRead: ms.cacheRead, CacheWrite: ms.cacheWrite,
			ToolCalls: ms.toolCalls, ToolErrors: ms.toolErrors,
			Statuses: nonEmpty(maps.Clone(ms.statuses)),
			Buckets:  make([]bucket, 0, len(ms.buckets)),
		}
		for _, b := range ms.buckets {
			copied := *b
			copied.TTFTBuckets = append([]uint64(nil), b.TTFTBuckets...)
			copied.E2EBuckets = append([]uint64(nil), b.E2EBuckets...)
			copied.ThroughputBuckets = append([]uint64(nil), b.ThroughputBuckets...)
			copied.StatusCodes = maps.Clone(b.StatusCodes)
			snapshot.Buckets = append(snapshot.Buckets, copied)
		}
		ms.mu.Unlock()
		sort.Slice(snapshot.Buckets, func(i, j int) bool {
			return snapshot.Buckets[i].WindowStart.Before(snapshot.Buckets[j].WindowStart)
		})
		models[key] = snapshot
	}
	return models
}

// persist flushes the in-memory model to PersistFile atomically (write temp in
// the same directory, then rename), mirroring auth.Store's pattern.
func (st *Stats) persist() {
	snap := st.snapshotForPersist()
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	path := st.cfg.PersistFile
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".stats-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return
	}
}

// load restores a previously saved snapshot into st, dropping expired buckets.
// Cumulative counters are set (not added to): the in-memory model starts from
// the snapshot's values.
func (st *Stats) load(path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap statsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	cutoff := retentionCutoff(time.Now(), st.cfg.RetentionDays)
	for key, ms := range snap.Models {
		m := &modelStats{
			requests:   ms.Requests,
			successes:  ms.Successes,
			tokensIn:   ms.TokensIn,
			tokensOut:  ms.TokensOut,
			cacheRead:  ms.CacheRead,
			cacheWrite: ms.CacheWrite,
			statuses:   maps.Clone(ms.Statuses),
			toolCalls:  ms.ToolCalls,
			toolErrors: ms.ToolErrors,
			buckets:    make(map[int64]*bucket, len(ms.Buckets)),
		}
		// Old v1 snapshots may omit lifetime status counters. Recover them from
		// all saved buckets before retention removes old observations.
		if m.statuses == nil {
			m.statuses = map[string]uint64{}
			for _, b := range ms.Buckets {
				for status, count := range b.StatusCodes {
					m.statuses[status] += count
				}
			}
		}
		for _, b := range ms.Buckets {
			win := b.WindowStart.Unix() / 300
			if cutoff > 0 && win < cutoff {
				continue
			}
			// Re-home the histogram buckets to the current edge counts in case
			// the edges changed between snapshot versions.
			b.TTFTBuckets = msBucketsOf(b.TTFTBuckets, ttftEdges)
			b.E2EBuckets = msBucketsOf(b.E2EBuckets, e2eEdges)
			b.ThroughputBuckets = msBucketsOf(b.ThroughputBuckets, tpsEdges)
			if b.StatusCodes == nil {
				b.StatusCodes = map[string]uint64{}
			}
			m.buckets[win] = &b
		}
		st.models[key] = m
	}
	return nil
}

// msBucketsOf returns src sliced to the number of edges, zero-padded if src
// is shorter (tolerates snapshots taken with a different edge set).
func msBucketsOf(src []uint64, edges []float64) []uint64 {
	n := len(edges)
	if len(src) >= n {
		return src[:n]
	}
	out := make([]uint64, n)
	copy(out, src)
	return out
}
