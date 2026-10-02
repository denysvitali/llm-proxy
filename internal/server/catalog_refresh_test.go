package server

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type catalogResult struct {
	models []string
	err    error
}

type controlledCatalogBackend struct {
	backend.Backend
	name    string
	calls   atomic.Int64
	results chan catalogResult
}

func newControlledCatalogBackend(name string) *controlledCatalogBackend {
	return &controlledCatalogBackend{name: name, results: make(chan catalogResult)}
}

func (b *controlledCatalogBackend) Name() string { return b.name }

func (b *controlledCatalogBackend) Models(ctx context.Context) ([]string, error) {
	b.calls.Add(1)
	select {
	case result := <-b.results:
		return result.models, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func startCatalogFetch(ctx context.Context, cache *catalogCache, b backend.Backend, log logrus.FieldLogger) <-chan catalogResult {
	result := make(chan catalogResult, 1)
	go func() {
		models, err := cache.get(ctx, b, log)
		result <- catalogResult{models: models, err: err}
	}()
	return result
}

func expiredCatalog(age time.Duration) cachedCatalog {
	return cachedCatalog{
		models: []string{"stale"}, expires: time.Now().Add(-time.Second), fetchedAt: time.Now().Add(-age),
	}
}

func TestCatalogRefreshShared(t *testing.T) {
	unavailable := errors.New("catalog unavailable")
	cases := []struct {
		name       string
		cachedAge  time.Duration
		refreshErr error
		want       []string
		wantErr    error
	}{
		{name: "cold success", want: []string{"fresh"}},
		{name: "cold failure", refreshErr: unavailable, wantErr: unavailable},
		{name: "stale success", cachedAge: time.Minute, want: []string{"fresh"}},
		{name: "stale fallback", cachedAge: time.Minute, refreshErr: unavailable, want: []string{"stale"}},
		{name: "beyond stale cap", cachedAge: catalogMaxStaleness, refreshErr: unavailable, wantErr: unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache := newCatalogCache()
				if tc.cachedAge > 0 {
					cache.entries["provider"] = expiredCatalog(tc.cachedAge)
				}
				b := newControlledCatalogBackend("provider")
				log := quietLogger()
				results := make([]<-chan catalogResult, 12)
				for i := range results {
					results[i] = startCatalogFetch(t.Context(), &cache, b, log)
				}
				// Every caller is now blocked either in Models or waiting for
				// that refresh. No scheduler timing or sleeps decide the test.
				synctest.Wait()
				if got := b.calls.Load(); got != 1 {
					t.Fatalf("concurrent Models calls = %d, want 1", got)
				}
				b.results <- catalogResult{models: []string{"fresh"}, err: tc.refreshErr}
				for _, result := range results {
					got := <-result
					if !reflect.DeepEqual(got.models, tc.want) || !errors.Is(got.err, tc.wantErr) {
						t.Errorf("result = %+v, want models=%v err=%v", got, tc.want, tc.wantErr)
					}
				}
			})
		})
	}
}

func TestCatalogRefreshDifferentBackends(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache catalogCache
		first, second := newControlledCatalogBackend("first"), newControlledCatalogBackend("second")
		firstResult := startCatalogFetch(t.Context(), &cache, first, quietLogger())
		secondResult := startCatalogFetch(t.Context(), &cache, second, quietLogger())
		synctest.Wait()
		if first.calls.Load() != 1 || second.calls.Load() != 1 {
			t.Fatal("unrelated catalog refreshes did not start concurrently")
		}
		second.results <- catalogResult{models: []string{"second-model"}}
		if got := <-secondResult; got.err != nil || !reflect.DeepEqual(got.models, []string{"second-model"}) {
			t.Fatalf("second provider = %+v", got)
		}
		select {
		case <-firstResult:
			t.Fatal("first provider finished before release")
		default:
		}
		first.results <- catalogResult{models: []string{"first-model"}}
		if got := <-firstResult; got.err != nil {
			t.Fatal(got.err)
		}
	})
}

func TestCatalogRefreshWaitingCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache catalogCache
		b := newControlledCatalogBackend("provider")
		leader := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := startCatalogFetch(ctx, &cache, b, quietLogger())
		healthy := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		cancel()
		if got := <-canceled; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled waiter = %+v", got)
		}
		b.results <- catalogResult{models: []string{"fresh"}}
		for _, result := range []<-chan catalogResult{leader, healthy} {
			if got := <-result; got.err != nil || !reflect.DeepEqual(got.models, []string{"fresh"}) {
				t.Errorf("healthy caller = %+v", got)
			}
		}
		if got := b.calls.Load(); got != 1 {
			t.Errorf("Models calls = %d, want 1", got)
		}
	})
}

func TestCatalogRefreshCanceledInitiatorRetries(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "cold"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache := newCatalogCache()
				if stale {
					cache.entries["provider"] = expiredCatalog(time.Minute)
				}
				b := newControlledCatalogBackend("provider")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				leader := startCatalogFetch(ctx, &cache, b, quietLogger())
				synctest.Wait()
				waiters := make([]<-chan catalogResult, 8)
				for i := range waiters {
					waiters[i] = startCatalogFetch(t.Context(), &cache, b, quietLogger())
				}
				synctest.Wait()
				cancel()
				if got := <-leader; !errors.Is(got.err, context.Canceled) || got.models != nil {
					t.Fatalf("canceled initiator = %+v", got)
				}
				synctest.Wait()
				if got := b.calls.Load(); got != 2 {
					t.Fatalf("healthy waiters started %d calls, want original plus one retry", got)
				}
				b.results <- catalogResult{models: []string{"fresh"}}
				for _, result := range waiters {
					if got := <-result; got.err != nil || !reflect.DeepEqual(got.models, []string{"fresh"}) {
						t.Errorf("healthy waiter inherited cancellation: %+v", got)
					}
				}
				if got, err := cache.get(t.Context(), b, quietLogger()); err != nil || !reflect.DeepEqual(got, []string{"fresh"}) {
					t.Errorf("retry cache = %v, %v", got, err)
				}
			})
		})
	}
}

func TestCatalogRefreshFailureRetriesAndThrottlesWarnings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newCatalogCache()
		cache.entries["provider"] = expiredCatalog(time.Minute)
		b := newControlledCatalogBackend("provider")
		log, hook := logtest.NewNullLogger()
		for i := range 2 {
			result := startCatalogFetch(t.Context(), &cache, b, log)
			synctest.Wait()
			if got := b.calls.Load(); got != int64(i+1) {
				t.Fatalf("Models calls = %d, want %d; failure was cached", got, i+1)
			}
			b.results <- catalogResult{err: errors.New("unavailable")}
			if got := <-result; got.err != nil || !reflect.DeepEqual(got.models, []string{"stale"}) {
				t.Errorf("stale fallback = %+v", got)
			}
		}
		if got := len(hook.AllEntries()); got != 1 {
			t.Errorf("stale warnings = %d, want 1", got)
		}
	})
}

func TestCatalogRefreshCrossingStalenessCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newCatalogCache()
		cache.entries["provider"] = expiredCatalog(catalogMaxStaleness - time.Second)
		b := newControlledCatalogBackend("provider")
		result := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		time.Sleep(2 * time.Second)
		unavailable := errors.New("unavailable")
		b.results <- catalogResult{err: unavailable}
		if got := <-result; !errors.Is(got.err, unavailable) || got.models != nil {
			t.Fatalf("over-age catalog served after slow failure: %+v", got)
		}
	})
}

func TestCatalogRefreshFreshnessStartsAtCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache catalogCache
		b := newControlledCatalogBackend("provider")
		result := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		time.Sleep(2 * catalogTTL)
		b.results <- catalogResult{models: []string{"fresh"}}
		if got := <-result; got.err != nil {
			t.Fatal(got.err)
		}
		// Even a refresh slower than the TTL produces a fresh cache entry.
		if got, err := cache.get(t.Context(), b, quietLogger()); err != nil || !reflect.DeepEqual(got, []string{"fresh"}) {
			t.Fatalf("completed refresh was not cached: %v, %v", got, err)
		}
		if got := b.calls.Load(); got != 1 {
			t.Errorf("slow refresh triggered another fetch: calls=%d", got)
		}
	})
}

type uncancelableCatalogBackend struct{ *controlledCatalogBackend }

func (b uncancelableCatalogBackend) Models(ctx context.Context) ([]string, error) {
	return b.controlledCatalogBackend.Models(context.WithoutCancel(ctx))
}

func TestCatalogRefreshDiscardsCanceledSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache catalogCache
		b := uncancelableCatalogBackend{newControlledCatalogBackend("provider")}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		leader := startCatalogFetch(ctx, &cache, b, quietLogger())
		synctest.Wait()
		waiter := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		cancel()
		b.results <- catalogResult{models: []string{"canceled-result"}}
		if got := <-leader; !errors.Is(got.err, context.Canceled) || got.models != nil {
			t.Fatalf("canceled success was returned: %+v", got)
		}
		synctest.Wait()
		if got := b.calls.Load(); got != 2 {
			t.Fatalf("canceled success was cached: Models calls=%d", got)
		}
		b.results <- catalogResult{models: []string{"fresh"}}
		if got := <-waiter; got.err != nil || !reflect.DeepEqual(got.models, []string{"fresh"}) {
			t.Fatalf("healthy waiter did not retry: %+v", got)
		}
	})
}

type panickingCatalogBackend struct {
	backend.Backend
	release chan struct{}
	calls   atomic.Int64
}

func (b *panickingCatalogBackend) Name() string { return "provider" }

func (b *panickingCatalogBackend) Models(context.Context) ([]string, error) {
	if b.calls.Add(1) == 1 {
		<-b.release
		panic("provider failed")
	}
	return []string{"recovered"}, nil
}

func TestCatalogRefreshPanicReleasesWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache catalogCache
		b := &panickingCatalogBackend{release: make(chan struct{})}
		panicResult := make(chan any, 1)
		go func() {
			defer func() { panicResult <- recover() }()
			_, _ = cache.get(t.Context(), b, quietLogger())
		}()
		synctest.Wait()
		waiter := startCatalogFetch(t.Context(), &cache, b, quietLogger())
		synctest.Wait()
		close(b.release)
		if got := <-panicResult; got != "provider failed" {
			t.Fatalf("initiating caller panic = %v", got)
		}
		if got := <-waiter; got.err != nil || !reflect.DeepEqual(got.models, []string{"recovered"}) {
			t.Fatalf("waiter failed to recover from initiator panic: %+v", got)
		}
	})
}
