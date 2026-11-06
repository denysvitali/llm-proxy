package workbuddy

import (
	"fmt"
	"time"
)

// loginPhase is the state of one browser login started by StartLogin and
// keyed by its state token in Manager.logins.
//
//	from          event                                    to
//	loginUnknown  StartLogin                               loginPending
//	loginPending  poll: token endpoint fails or is empty   loginPending
//	loginPending  poll: token returned and session saved   loginAuthorized (entry deleted)
//	loginPending  loginTTL passes                          loginExpired
//	loginExpired  next PollLogin deletes the entry         loginUnknown
//
// Expired entries are only removed when polled.
type loginPhase int

const (
	loginUnknown loginPhase = iota
	loginPending
	loginExpired
	loginAuthorized
)

func (p loginPhase) String() string {
	switch p {
	case loginUnknown:
		return "unknown"
	case loginPending:
		return "pending"
	case loginExpired:
		return "expired"
	case loginAuthorized:
		return "authorized"
	default:
		return fmt.Sprintf("loginPhase(%d)", int(p))
	}
}

// phaseAt reports a stored login's phase; a nil login is loginUnknown.
// loginAuthorized is never stored: the entry is deleted on that transition.
func (l *loginState) phaseAt(now time.Time) loginPhase {
	switch {
	case l == nil:
		return loginUnknown
	case now.After(l.expires):
		return loginExpired
	default:
		return loginPending
	}
}

// sessionState classifies a loaded WorkBuddy session. A missing or tokenless
// session file is reported by load as an error before classification.
//
//	from             event                                 to
//	sessionActive    clock reaches 5 min before ExpiresAt  sessionExpiring
//	sessionExpiring  refresh succeeds and is saved         sessionActive
//	sessionExpiring  (no refresh token)                    (error: sign in again)
//
// A zero ExpiresAt never expires.
type sessionState int

const (
	sessionActive sessionState = iota
	sessionExpiring
)

func (s sessionState) String() string {
	switch s {
	case sessionActive:
		return "active"
	case sessionExpiring:
		return "expiring"
	default:
		return fmt.Sprintf("sessionState(%d)", int(s))
	}
}

const sessionExpiryWindow = 5 * time.Minute

func classifySession(credentials Credentials, now time.Time) sessionState {
	if credentials.ExpiresAt > 0 && credentials.ExpiresAt <= now.Add(sessionExpiryWindow).Unix() {
		return sessionExpiring
	}
	return sessionActive
}

// cacheState is the freshness of the model catalog cache.
//
//	from        event             to
//	cacheEmpty  successful fetch  cacheFresh
//	cacheFresh  modelCacheTTL     cacheStale
//	cacheStale  successful fetch  cacheFresh
//	cacheStale  failed fetch      cacheStale (previous catalog served)
//	cacheEmpty  failed fetch      cacheEmpty (error)
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

// modelCacheState reports the catalog cache state. c.modelMu must be held.
func (c *Client) modelCacheState(now time.Time) cacheState {
	switch {
	case len(c.cachedModels) == 0:
		return cacheEmpty
	case now.Before(c.modelsUntil):
		return cacheFresh
	default:
		return cacheStale
	}
}
