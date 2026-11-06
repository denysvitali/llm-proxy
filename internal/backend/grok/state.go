package grok

import (
	"fmt"
	"strings"
	"time"
)

// sessionState classifies the xAI account session held by the store.
//
//	from              event                                        to
//	sessionUnsaved    legacy grok-subscription-client file imports  sessionActive/sessionExpiring
//	sessionUnsaved    ~/.grok/auth.json imports                    sessionActive/sessionExpiring
//	sessionUnsaved    nothing to import                            (error: not signed in)
//	sessionSignedOut  (saved file without an access token)         (error: not signed in)
//	sessionActive     clock reaches 5 min before ExpiresAt         sessionExpiring
//	sessionExpiring   refresh succeeds and is saved                sessionActive
//	sessionExpiring   (no refresh token)                           (error: sign in again)
//
// The legacy import runs lazily, only on a request that finds no saved file.
// A zero ExpiresAt never expires.
type sessionState int

const (
	sessionUnsaved sessionState = iota
	sessionSignedOut
	sessionActive
	sessionExpiring
)

func (s sessionState) String() string {
	switch s {
	case sessionUnsaved:
		return "unsaved"
	case sessionSignedOut:
		return "signed_out"
	case sessionActive:
		return "active"
	case sessionExpiring:
		return "expiring"
	default:
		return fmt.Sprintf("sessionState(%d)", int(s))
	}
}

// sessionExpiryWindow is how long before expiry a session is refreshed.
const sessionExpiryWindow = 5 * time.Minute

func classifySession(token *Token, now time.Time) sessionState {
	switch {
	case token == nil:
		return sessionUnsaved
	case token.AccessToken == "":
		return sessionSignedOut
	case token.ExpiresAt > 0 && token.ExpiresAt <= float64(now.Add(sessionExpiryWindow).Unix()):
		return sessionExpiring
	default:
		return sessionActive
	}
}

// devicePoll is the result of one xAI device-token poll. Each poll waits the
// current interval first.
//
//	from           poll result                        to
//	devicePending  error containing authorization_pending  devicePending
//	devicePending  error containing slow_down         deviceSlowDown (interval += 5s)
//	deviceSlowDown (next poll)                        as devicePending
//	devicePending  token response                     deviceAuthorized (save session)
//	devicePending  any other error                    deviceFailed (error)
//	devicePending  device expires_in passes           expired (error)
type devicePoll int

const (
	devicePending devicePoll = iota
	deviceSlowDown
	deviceAuthorized
	deviceFailed
)

func (p devicePoll) String() string {
	switch p {
	case devicePending:
		return "pending"
	case deviceSlowDown:
		return "slow_down"
	case deviceAuthorized:
		return "authorized"
	case deviceFailed:
		return "failed"
	default:
		return fmt.Sprintf("devicePoll(%d)", int(p))
	}
}

func classifyDevicePoll(err error) devicePoll {
	switch {
	case err == nil:
		return deviceAuthorized
	case strings.Contains(err.Error(), "authorization_pending"):
		return devicePending
	case strings.Contains(err.Error(), "slow_down"):
		return deviceSlowDown
	default:
		return deviceFailed
	}
}
