package server

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyCatalog(t *testing.T) {
	now := time.Now()
	held := catalogFailure{err: errors.New("down"), until: now.Add(time.Second)}
	lapsed := catalogFailure{err: errors.New("down"), until: now.Add(-time.Second)}
	fresh := cachedCatalog{expires: now.Add(time.Second), fetchedAt: now}
	stale := cachedCatalog{expires: now.Add(-time.Second), fetchedAt: now.Add(-time.Minute)}
	old := cachedCatalog{expires: now.Add(-time.Second), fetchedAt: now.Add(-catalogMaxStaleness)}
	cases := []struct {
		name       string
		entry      cachedCatalog
		hasEntry   bool
		failure    catalogFailure
		hasFailure bool
		want       catalogEntryState
	}{
		{name: "cold", want: catalogCold},
		{name: "fresh", entry: fresh, hasEntry: true, want: catalogFresh},
		{name: "stale servable", entry: stale, hasEntry: true, want: catalogStaleServable},
		{name: "beyond cap", entry: old, hasEntry: true, want: catalogExpired},
		{name: "failure held", failure: held, hasFailure: true, want: catalogFailedHeld},
		{name: "failure lapsed", failure: lapsed, hasFailure: true, want: catalogCold},
		{name: "entry shadows failure", entry: stale, hasEntry: true, failure: held, hasFailure: true, want: catalogStaleServable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCatalog(tc.entry, tc.hasEntry, tc.failure, tc.hasFailure, now)
			if got != tc.want {
				t.Fatalf("classifyCatalog = %v, want %v", got, tc.want)
			}
			if got.needsRefresh() != (got != catalogFresh && got != catalogFailedHeld) {
				t.Fatalf("%v needsRefresh = %v", got, got.needsRefresh())
			}
		})
	}
}

func TestCatalogRefreshTransitions(t *testing.T) {
	failed := errors.New("down")
	cases := []struct {
		name     string
		publish  bool
		canceled bool
		want     catalogRefreshState
	}{
		{name: "published", publish: true, want: catalogRefreshPublished},
		{name: "canceled leader", publish: true, canceled: true, want: catalogRefreshAbandoned},
		{name: "panicked leader", want: catalogRefreshAbandoned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCatalogRefresh()
			if r.state != catalogRefreshRunning || !r.shouldRetry() {
				t.Fatalf("new refresh state = %v", r.state)
			}
			if tc.publish {
				r.publish([]string{"m"}, failed, tc.canceled)
			}
			r.finish()
			select {
			case <-r.done:
			default:
				t.Fatal("finish did not release waiters")
			}
			if r.state != tc.want {
				t.Fatalf("state = %v, want %v", r.state, tc.want)
			}
			if r.shouldRetry() != (tc.want == catalogRefreshAbandoned) {
				t.Fatalf("shouldRetry = %v in %v", r.shouldRetry(), r.state)
			}
			// A second publish after the terminal state is ignored.
			r.publish(nil, nil, false)
			if r.state != tc.want {
				t.Fatalf("publish after %v moved state to %v", tc.want, r.state)
			}
		})
	}
}
