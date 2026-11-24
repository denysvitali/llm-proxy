package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	zaibackend "github.com/denysvitali/llm-proxy/internal/backend/zai"
	"github.com/denysvitali/llm-proxy/internal/config"
)

const zaiUsageTTL = time.Minute

var errZaiUsageUnavailable = errors.New("z.ai usage needs an enabled zai backend with an API key")

// zaiUsageCache keeps one short-lived Coding Plan snapshot; failed refreshes
// keep the previous value and return the error. The cached value is never
// mutated after a fetch; readers only serialize it.
type zaiUsageCache struct {
	fetch func(context.Context) (zaibackend.Usage, error)
	mu    sync.Mutex
	value *zaibackend.Usage
}

func newZaiUsageCache(backends []config.BackendConfig) *zaiUsageCache {
	for _, bc := range backends {
		if bc.Type != "zai" || !bc.IsEnabled() {
			continue
		}
		key := bc.ResolveKey(os.Getenv)
		if key == "" {
			return nil
		}
		return &zaiUsageCache{fetch: zaibackend.New(bc.BaseURL, key).Usage}
	}
	return nil
}

func (c *zaiUsageCache) get(ctx context.Context) (zaibackend.Usage, error) {
	if c == nil || c.fetch == nil {
		return zaibackend.Usage{}, errZaiUsageUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil && usageSnapshotStateAt(true, c.value.FetchedAt, zaiUsageTTL, time.Now()) == usageSnapshotFresh {
		return *c.value, nil
	}
	usage, err := c.fetch(ctx)
	if err != nil {
		return zaibackend.Usage{}, err
	}
	c.value = &usage
	return usage, nil
}

func (s *Server) handleZaiUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	usage, err := s.zaiUsage.get(ctx)
	switch {
	case errors.Is(err, errZaiUsageUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, zaibackend.ErrUsageUnauthorized):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Z.ai rejected the API key; check api_key_env."})
	case err != nil:
		s.log.WithError(err).Warn("z.ai usage fetch failed")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sanitizeUsageError(err)})
	default:
		if usage.Windows == nil {
			usage.Windows = []zaibackend.Window{}
		}
		writeJSON(w, http.StatusOK, usage)
	}
}

// zaiUsageMetadata stays local so a Z.ai outage cannot delay the dashboard shell.
func (s *Server) zaiUsageMetadata() usageMetadata {
	if s.zaiUsage == nil {
		return usageUnconfigured.metadata("")
	}
	return usageAvailable.metadata("")
}
