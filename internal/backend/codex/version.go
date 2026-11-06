package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const versionRegistryURL = "https://registry.npmjs.org/@openai%2fcodex/latest"

// Use an independent client so account credentials, cookies, and inference
// transports never accompany public registry requests.
var stableCodexVersion = &versionDiscovery{
	client:   &http.Client{Timeout: 2 * time.Second},
	endpoint: versionRegistryURL,
}

var stableVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// versionDiscovery caches the latest stable Codex CLI version. version is
// never empty once latest has run (it starts at the clientVersion fallback),
// so the cache is only ever cacheFresh or cacheStale:
//
//	from        event                                          to
//	(unset)     first latest call                              fallback version, cacheStale
//	cacheStale  lookup starts                                  cacheFresh for versionFailureTTL
//	            (a failed lookup keeps this provisional deadline and the previous version)
//	cacheFresh  lookup succeeds                                cacheFresh for versionSuccessTTL, new version
//	cacheFresh  caller's context ends during the lookup        previous deadline restored
//	cacheFresh  deadline passes                                cacheStale
type versionDiscovery struct {
	mu       sync.Mutex
	client   *http.Client
	endpoint string
	version  string
	until    time.Time
}

const (
	versionFailureTTL = 5 * time.Minute
	versionSuccessTTL = time.Hour
)

func (v *versionDiscovery) stateAt(now time.Time) cacheState {
	return cacheStateAt(v.version != "", v.until, now)
}

func (v *versionDiscovery) latest(ctx context.Context) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.version == "" {
		v.version = clientVersion
	}
	if v.stateAt(time.Now()) == cacheFresh {
		return v.version
	}
	// Cache failures as well, while retaining the last successfully discovered
	// version. Registry outages must not prevent catalog retrieval.
	previousUntil := v.until
	parentCtx := ctx
	defer func() {
		// A caller leaving is not evidence of a registry outage. Allow the
		// next healthy caller to retry immediately instead of caching failure.
		if parentCtx.Err() != nil {
			v.until = previousUntil
		}
	}()
	v.until = time.Now().Add(versionFailureTTL)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return v.version
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return v.version
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return v.version
	}
	var payload struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload) != nil || len(payload.Version) > 64 || !stableVersionPattern.MatchString(payload.Version) {
		return v.version
	}
	v.version = payload.Version
	v.until = time.Now().Add(versionSuccessTTL)
	return v.version
}
