package codex

import (
	"errors"
	"fmt"
	"time"
)

// sessionState classifies the saved ChatGPT session at one point in time.
//
//	from              event                                          to
//	sessionSignedOut  LoginDevice saves tokens                       sessionActive
//	sessionActive     clock reaches 5 min before JWT exp             sessionExpiring
//	sessionActive     upstream 401 for this exact access token       sessionRejected
//	sessionExpiring   refresh under the file lock after a re-read    sessionActive
//	sessionRejected   refresh under the file lock                    sessionActive
//	sessionExpiring   (no refresh token)                             error: sign in again
//	sessionRejected   (no refresh token)                             error: sign in again
//
// Credentials re-classifies after taking the cross-replica file lock, so a
// session another replica already refreshed is used without a second refresh.
// ForceRefresh treats a session whose token no longer equals the rejected one
// as sessionActive (it was replaced concurrently) and does not refresh.
type sessionState int

const (
	sessionSignedOut sessionState = iota
	sessionActive
	sessionExpiring
	sessionRejected
)

func (s sessionState) String() string {
	switch s {
	case sessionSignedOut:
		return "signed_out"
	case sessionActive:
		return "active"
	case sessionExpiring:
		return "expiring"
	case sessionRejected:
		return "rejected"
	default:
		return fmt.Sprintf("sessionState(%d)", int(s))
	}
}

// sessionExpiryWindow is how long before JWT expiry a session is refreshed.
const sessionExpiryWindow = 5 * time.Minute

// classifySession reports the time-based state of a loaded session. When
// ExpiresAt is unset it is filled from the access token's JWT exp claim; a
// token without a decodable expiry never becomes sessionExpiring.
func classifySession(credentials *Credentials) sessionState {
	if credentials == nil || credentials.AccessToken == "" {
		return sessionSignedOut
	}
	if expiring(credentials) {
		return sessionExpiring
	}
	return sessionActive
}

// classifyRejectedSession reports the state of a loaded session after
// upstream rejected the access token stale.
func classifyRejectedSession(credentials *Credentials, stale string) sessionState {
	if credentials == nil || credentials.AccessToken == "" {
		return sessionSignedOut
	}
	if credentials.AccessToken == stale {
		return sessionRejected
	}
	return sessionActive
}

func errNotSignedIn() error {
	return errors.New("not signed in to codex; use the dashboard to sign in with ChatGPT")
}

func errSessionExpired() error {
	return errors.New("codex session expired; sign in again from the dashboard")
}

// unauthorizedRetry is the refresh-once policy applied when upstream answers
// 401 (before any inference ran):
//
//	from                event                                    to
//	retryFirstAttempt   2xx/other status                         (done)
//	retryFirstAttempt   401, token source cannot force-refresh   (done; 401 relayed)
//	retryFirstAttempt   401, ForceRefresh fails                  (done; error)
//	retryFirstAttempt   401, ForceRefresh succeeds               retryRefreshed
//	retryRefreshed      any response, including another 401      (done; relayed)
type unauthorizedRetry int

const (
	retryFirstAttempt unauthorizedRetry = iota
	retryRefreshed
)

func (r unauthorizedRetry) String() string {
	switch r {
	case retryFirstAttempt:
		return "first_attempt"
	case retryRefreshed:
		return "refreshed"
	default:
		return fmt.Sprintf("unauthorizedRetry(%d)", int(r))
	}
}

// cacheState is the freshness of a TTL cache entry (the model catalog and
// the discovered client version).
//
//	from        event                         to
//	cacheEmpty  successful fetch              cacheFresh
//	cacheFresh  TTL deadline passes           cacheStale
//	cacheStale  successful fetch              cacheFresh
//	cacheStale  failed fetch                  cacheStale (previous value served)
//	cacheEmpty  failed fetch                  cacheEmpty (error, or fallback)
type cacheState int

const (
	cacheEmpty cacheState = iota
	cacheFresh
	cacheStale
)

func (s cacheState) String() string {
	switch s {
	case cacheEmpty:
		return "empty"
	case cacheFresh:
		return "fresh"
	case cacheStale:
		return "stale"
	default:
		return fmt.Sprintf("cacheState(%d)", int(s))
	}
}

func cacheStateAt(present bool, until, now time.Time) cacheState {
	switch {
	case !present:
		return cacheEmpty
	case now.Before(until):
		return cacheFresh
	default:
		return cacheStale
	}
}
