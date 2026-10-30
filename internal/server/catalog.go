package server

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/sirupsen/logrus"
)

type cachedCatalog struct {
	models    []string
	expires   time.Time
	fetchedAt time.Time
	lastWarn  time.Time
}

// catalogRefresh shares one attempt among callers for the same backend.
// Closing done publishes the result. A canceled initiator leaves retry set so
// healthy waiters can refresh using their own contexts.
type catalogRefresh struct {
	done   chan struct{}
	models []string
	err    error
	retry  bool
}

const (
	// catalogTTL is how long a fetched model list is considered fresh.
	catalogTTL = time.Minute
	// catalogMaxStaleness is the upper bound on how long a stale catalog
	// entry may be served while a backend's /models endpoint is failing (10x
	// the TTL). Beyond it the refresh error propagates so a permanently
	// broken catalog endpoint does not go unnoticed.
	catalogMaxStaleness = 10 * catalogTTL
	// catalogStaleWarnInterval throttles the "serving stale catalog" warning
	// to one line per backend per interval; without it every request past the
	// TTL logs while the endpoint is failing.
	catalogStaleWarnInterval = 5 * time.Minute
	// catalogFailureTTL holds a failed fetch with no usable cache, so a
	// backend with a dead credential is not re-queried by every request
	// that scans catalogs for routing.
	catalogFailureTTL = 30 * time.Second
	// catalogFailureWarnInterval throttles "catalog fetch failed" warnings to
	// one line per backend per interval.
	catalogFailureWarnInterval = 5 * time.Minute
)

type catalogFailure struct {
	err   error
	until time.Time
}

// catalogCache owns model discovery state shared by routing and HTTP catalogs.
// Its zero value is ready for use. Entries remain fresh for catalogTTL; an
// expired entry can survive refresh failures up to catalogMaxStaleness.
type catalogCache struct {
	mu      sync.Mutex
	entries map[string]cachedCatalog
	refresh map[string]*catalogRefresh
	ttl     time.Duration
	// failures caches recent fetch errors; failureWarned throttles warnings.
	failures      map[string]catalogFailure
	failureWarned map[string]time.Time
}

func newCatalogCache() catalogCache {
	return catalogCache{entries: map[string]cachedCatalog{}, ttl: catalogTTL}
}

func (s *Server) catalog(ctx context.Context, b backend.Backend) ([]string, error) {
	return s.catalogs.get(ctx, b, s.log)
}

// backendCatalog shares the routing cache with model-list and overview APIs.
func (s *Server) backendCatalog(ctx context.Context, b backend.Backend) ([]string, error) {
	return s.catalog(ctx, b)
}

func (c *catalogCache) get(ctx context.Context, b backend.Backend, log logrus.FieldLogger) ([]string, error) {
	name := b.Name()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[string]cachedCatalog)
		}
		if c.refresh == nil {
			c.refresh = make(map[string]*catalogRefresh)
		}
		if c.ttl <= 0 {
			c.ttl = catalogTTL
		}
		cached, ok := c.entries[name]
		if ok && time.Now().Before(cached.expires) {
			c.mu.Unlock()
			return cached.models, nil
		}
		if failed, held := c.failures[name]; held && !ok && time.Now().Before(failed.until) {
			c.mu.Unlock()
			return nil, failed.err
		}
		if running := c.refresh[name]; running != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-running.done:
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if running.retry {
				continue
			}
			return running.models, running.err
		}
		running := &catalogRefresh{done: make(chan struct{}), retry: true}
		c.refresh[name] = running
		c.mu.Unlock()
		// Also release waiters if provider code panics. The panic continues
		// to the caller; retry stays set until a normal result is published.
		defer func() {
			c.mu.Lock()
			delete(c.refresh, name)
			close(running.done)
			c.mu.Unlock()
		}()

		// The initiating request owns this synchronous fetch. Cancellation
		// never detaches provider work from the request that started it.
		running.models, running.err = c.fetch(ctx, b, log, cached, ok)
		running.retry = ctx.Err() != nil
		if running.err != nil && !running.retry && !ok {
			c.mu.Lock()
			if c.failures == nil {
				c.failures = make(map[string]catalogFailure)
			}
			c.failures[name] = catalogFailure{err: running.err, until: time.Now().Add(catalogFailureTTL)}
			c.mu.Unlock()
		}
		return running.models, running.err
	}
}

func (c *catalogCache) fetch(ctx context.Context, b backend.Backend, log logrus.FieldLogger, cached cachedCatalog, hasCached bool) ([]string, error) {
	models, err := b.Models(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	now := time.Now()
	if err != nil {
		if hasCached {
			// Age is measured from when the entry was last refreshed.
			age := now.Sub(cached.fetchedAt)
			if age < catalogMaxStaleness {
				if now.Sub(cached.lastWarn) >= catalogStaleWarnInterval {
					c.mu.Lock()
					if existing, still := c.entries[b.Name()]; still {
						existing.lastWarn = now
						c.entries[b.Name()] = existing
					}
					c.mu.Unlock()
					log.WithField("backend", b.Name()).
						WithField("age", age.Round(time.Second)).
						Warn("serving stale model catalog; backend /models is failing")
				}
				return cached.models, nil
			}
			log.WithField("backend", b.Name()).
				WithField("age", age.Round(time.Second)).
				Warn("stale model catalog exceeds maximum age; propagating backend error")
		}
		return nil, err
	}

	c.mu.Lock()
	delete(c.failures, b.Name())
	c.entries[b.Name()] = cachedCatalog{
		models:    models,
		expires:   now.Add(catalogJitter(c.ttl)),
		fetchedAt: now,
	}
	c.mu.Unlock()
	return models, nil
}

// catalogJitter stretches a TTL by up to ±10% so several backends refreshed
// around the same moment do not re-fetch in lockstep forever after.
func catalogJitter(ttl time.Duration) time.Duration {
	spread := ttl / 10
	if spread <= 0 {
		return ttl
	}
	return ttl + time.Duration(rand.Int64N(int64(2*spread)+1)) - spread
}

// shouldWarnFailure reports whether a catalog failure for name should be
// logged at warning level now, claiming the slot for this interval.
func (c *catalogCache) shouldWarnFailure(name string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failureWarned == nil {
		c.failureWarned = make(map[string]time.Time)
	}
	if last, ok := c.failureWarned[name]; ok && now.Sub(last) < catalogFailureWarnInterval {
		return false
	}
	c.failureWarned[name] = now
	return true
}

// logCatalogFailure reports a failed catalog lookup. A caller that went away
// is not a backend failure, and repeated failures of one backend log at
// warning level once per catalogFailureWarnInterval.
func (s *Server) logCatalogFailure(ctx context.Context, name string, err error) {
	entry := s.log.WithError(err).WithField("backend", name)
	if ctx.Err() != nil {
		entry.Debug("catalog fetch abandoned; caller canceled")
		return
	}
	if !s.catalogs.shouldWarnFailure(name, time.Now()) {
		entry.Debug("catalog fetch failed")
		return
	}
	entry.Warn("catalog fetch failed")
}
