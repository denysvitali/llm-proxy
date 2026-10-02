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
)

// catalogCache owns model discovery state shared by routing and HTTP catalogs.
// Its zero value is ready for use. Entries remain fresh for catalogTTL; an
// expired entry can survive refresh failures up to catalogMaxStaleness.
type catalogCache struct {
	mu      sync.Mutex
	entries map[string]cachedCatalog
	ttl     time.Duration
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
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]cachedCatalog)
	}
	if c.ttl <= 0 {
		c.ttl = catalogTTL
	}
	cached, ok := c.entries[b.Name()]
	c.mu.Unlock()

	now := time.Now()
	if ok && now.Before(cached.expires) {
		return cached.models, nil
	}

	models, err := b.Models(ctx)
	if err != nil {
		if ok {
			// Age is measured from when the entry was last refreshed.
			age := now.Sub(cached.fetchedAt)
			if age < catalogMaxStaleness {
				if now.Sub(cached.lastWarn) >= catalogStaleWarnInterval {
					cached.lastWarn = now
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
