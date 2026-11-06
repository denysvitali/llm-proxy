package workbuddy

import (
	"testing"
	"time"
)

func TestLoginPhaseAt(t *testing.T) {
	now := time.Now()
	var missing *loginState
	if got := missing.phaseAt(now); got != loginUnknown {
		t.Fatalf("nil login = %v", got)
	}
	if got := (&loginState{expires: now}).phaseAt(now); got != loginPending {
		t.Fatalf("login at expiry instant = %v, want pending", got)
	}
	if got := (&loginState{expires: now.Add(-time.Nanosecond)}).phaseAt(now); got != loginExpired {
		t.Fatalf("login after expiry = %v, want expired", got)
	}
}

func TestPollLoginExpiredEntryIsDeleted(t *testing.T) {
	m := NewManager(t.TempDir() + "/session.json")
	m.logins["s"] = &loginState{client: newAuthClient(), expires: time.Now().Add(-time.Second)}
	if ok, err := m.PollLogin(t.Context(), "s"); ok || err == nil || err.Error() != "WorkBuddy login expired; start again" {
		t.Fatalf("PollLogin expired = %v, %v", ok, err)
	}
	if ok, err := m.PollLogin(t.Context(), "s"); ok || err == nil || err.Error() != "unknown WorkBuddy login; start again" {
		t.Fatalf("PollLogin after expiry = %v, %v", ok, err)
	}
}

func TestClassifySessionAndModelCache(t *testing.T) {
	now := time.Now()
	if got := classifySession(Credentials{}, now); got != sessionActive {
		t.Fatalf("zero expiry = %v", got)
	}
	if got := classifySession(Credentials{ExpiresAt: now.Add(time.Minute).Unix()}, now); got != sessionExpiring {
		t.Fatalf("inside window = %v", got)
	}
	c := &Client{}
	if got := c.modelCacheState(now); got != cacheEmpty {
		t.Fatalf("empty cache = %v", got)
	}
	c.cachedModels, c.modelsUntil = []string{"m"}, now.Add(time.Second)
	if got := c.modelCacheState(now); got != cacheFresh {
		t.Fatalf("fresh cache = %v", got)
	}
	if got := c.modelCacheState(now.Add(time.Second)); got != cacheStale {
		t.Fatalf("stale cache = %v", got)
	}
}
