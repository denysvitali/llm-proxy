package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	opendesignbackend "github.com/denysvitali/llm-proxy/internal/backend/opendesign"
	"github.com/denysvitali/llm-proxy/internal/config"
)

const openDesignUsageTTL = time.Minute

// openDesignWindowView is one Coding Plan window shaped for the dashboard.
// Dollar amounts are nil when the account did not state a credits rate.
type openDesignWindowView struct {
	Label            string     `json:"label"`
	DurationSeconds  int64      `json:"durationSeconds"`
	ResetMode        string     `json:"resetMode,omitempty"`
	UsedPercent      float64    `json:"usedPercent"`
	RemainingPercent float64    `json:"remainingPercent"`
	UsedCredits      int64      `json:"usedCredits"`
	LimitCredits     int64      `json:"limitCredits"`
	RemainingCredits int64      `json:"remainingCredits"`
	UsedUSD          *float64   `json:"usedUsd,omitempty"`
	LimitUSD         *float64   `json:"limitUsd,omitempty"`
	RemainingUSD     *float64   `json:"remainingUsd,omitempty"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
}

type openDesignUsageView struct {
	Tier       string                 `json:"tier,omitempty"`
	Eligible   bool                   `json:"eligible"`
	Funding    string                 `json:"funding,omitempty"`
	BalanceUSD string                 `json:"balanceUsd,omitempty"`
	Windows    []openDesignWindowView `json:"windows"`
	FetchedAt  time.Time              `json:"fetchedAt"`
}

// openDesignUsageCache keeps one short-lived snapshot; failed refreshes keep
// the previous value and return the error.
type openDesignUsageCache struct {
	fetch func(context.Context) (opendesignbackend.Usage, error)
	mu    sync.Mutex
	value *openDesignUsageView
}

func newOpenDesignUsageCache(backends []config.BackendConfig) *openDesignUsageCache {
	for _, bc := range backends {
		if bc.Type != "opendesign" || !bc.IsEnabled() {
			continue
		}
		key := bc.ResolveUsageKey(os.Getenv)
		if key == "" {
			return nil
		}
		client := opendesignbackend.NewUsageClient("", key, bc.WorkspaceID)
		return &openDesignUsageCache{fetch: client.Usage}
	}
	return nil
}

func (c *openDesignUsageCache) get(ctx context.Context) (openDesignUsageView, error) {
	if c == nil || c.fetch == nil {
		return openDesignUsageView{}, errOpenDesignUsageUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state(time.Now()) == usageSnapshotFresh {
		return cloneOpenDesignUsage(*c.value), nil
	}
	usage, err := c.fetch(ctx)
	if err != nil {
		return openDesignUsageView{}, err
	}
	view := newOpenDesignUsageView(usage, time.Now().UTC())
	c.value = &view
	return cloneOpenDesignUsage(view), nil
}

func (c *openDesignUsageCache) state(now time.Time) usageSnapshotState {
	if c.value == nil {
		return usageSnapshotEmpty
	}
	return usageSnapshotStateAt(true, c.value.FetchedAt, openDesignUsageTTL, now)
}

var errOpenDesignUsageUnavailable = errors.New("open design plan usage is not configured; set usage_key_env to the account control key")

func newOpenDesignUsageView(usage opendesignbackend.Usage, now time.Time) openDesignUsageView {
	view := openDesignUsageView{
		Tier: usage.Tier, Eligible: usage.Eligible, Funding: usage.Funding, BalanceUSD: usage.BalanceUSD,
		Windows: make([]openDesignWindowView, 0, len(usage.Windows)), FetchedAt: now,
	}
	dollars := func(credits int64) *float64 {
		if usage.CreditsPerUSD <= 0 {
			return nil
		}
		value := float64(credits) / usage.CreditsPerUSD
		return &value
	}
	for _, w := range usage.Windows {
		used := float64(w.UsedCredits) / float64(w.LimitCredits) * 100
		remaining := float64(w.RemainingCredits) / float64(w.LimitCredits) * 100
		view.Windows = append(view.Windows, openDesignWindowView{
			Label: windowLabel(w.DurationSeconds), DurationSeconds: w.DurationSeconds, ResetMode: w.ResetMode,
			// Overuse can exceed 100% used; keep it visible rather than clamp.
			UsedPercent: used, RemainingPercent: remaining,
			UsedCredits: w.UsedCredits, LimitCredits: w.LimitCredits, RemainingCredits: w.RemainingCredits,
			UsedUSD: dollars(w.UsedCredits), LimitUSD: dollars(w.LimitCredits), RemainingUSD: dollars(w.RemainingCredits),
			ResetsAt: w.ResetsAt,
		})
	}
	return view
}

// windowLabel names a window by its length: 5 hours → "5-hour",
// 7 days → "Weekly".
func windowLabel(seconds int64) string {
	switch {
	case seconds == 7*86400:
		return "Weekly"
	case seconds == 86400:
		return "Daily"
	case seconds%86400 == 0:
		return fmt.Sprintf("%d-day", seconds/86400)
	case seconds%3600 == 0:
		return fmt.Sprintf("%d-hour", seconds/3600)
	default:
		return fmt.Sprintf("%d-minute", (seconds+59)/60)
	}
}

func cloneOpenDesignUsage(view openDesignUsageView) openDesignUsageView {
	windows := make([]openDesignWindowView, len(view.Windows))
	for i, w := range view.Windows {
		for _, amount := range []**float64{&w.UsedUSD, &w.LimitUSD, &w.RemainingUSD} {
			if *amount != nil {
				value := **amount
				*amount = &value
			}
		}
		if w.ResetsAt != nil {
			at := *w.ResetsAt
			w.ResetsAt = &at
		}
		windows[i] = w
	}
	view.Windows = windows
	return view
}

func (s *Server) handleOpenDesignUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	usage, err := s.openDesignUsage.get(ctx)
	switch {
	case errors.Is(err, errOpenDesignUsageUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, opendesignbackend.ErrUsageUnauthorized):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Open Design rejected the account control key; replace usage_key_env's secret."})
	case err != nil:
		s.log.WithError(err).Warn("open design usage fetch failed")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sanitizeUsageError(err)})
	default:
		writeJSON(w, http.StatusOK, usage)
	}
}

// openDesignUsageMetadata stays local so an account API outage cannot delay
// the dashboard shell.
func (s *Server) openDesignUsageMetadata() usageMetadata {
	if s.openDesignUsage == nil {
		return usageUnconfigured.metadata("")
	}
	return usageAvailable.metadata("")
}
