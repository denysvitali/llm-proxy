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

type versionDiscovery struct {
	mu       sync.Mutex
	client   *http.Client
	endpoint string
	version  string
	until    time.Time
}

func (v *versionDiscovery) latest(ctx context.Context) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.version == "" {
		v.version = clientVersion
	}
	if time.Now().Before(v.until) {
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
	v.until = time.Now().Add(5 * time.Minute)
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
	v.until = time.Now().Add(time.Hour)
	return v.version
}
