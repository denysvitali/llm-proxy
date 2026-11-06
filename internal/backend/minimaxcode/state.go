package minimaxcode

import (
	"fmt"
	"time"
)

// sessionState classifies the saved MiniMax Code OAuth session.
//
//	from              event                                   to
//	sessionSignedOut  LoginDevice saves tokens                sessionActive
//	sessionActive     clock reaches 5 min before ExpiresAt    sessionExpiring
//	sessionExpiring   refresh succeeds and is saved           sessionActive
//	sessionExpiring   no refresh token                        (Terminal: session expired)
//	sessionExpiring   token endpoint rejects the refresh      (Terminal: sign in again)
//	sessionExpiring   transport/HTTP failure                  sessionExpiring (retryable error)
//
// A zero ExpiresAt never expires. sessionSignedOut is Terminal for requests.
type sessionState int

const (
	sessionSignedOut sessionState = iota
	sessionActive
	sessionExpiring
)

func (s sessionState) String() string {
	switch s {
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

const sessionExpiryWindow = 5 * time.Minute

func classifySession(credentials *Credentials, now time.Time) sessionState {
	switch {
	case credentials == nil || credentials.AccessToken == "":
		return sessionSignedOut
	case credentials.ExpiresAt > 0 && credentials.ExpiresAt <= now.Add(sessionExpiryWindow).Unix():
		return sessionExpiring
	default:
		return sessionActive
	}
}

// deviceProtocol selects how the device flow identifies the pending sign-in.
// MiniMax Code builds before the standard flow return no device_code, only a
// user_code with a future expired_in timestamp in milliseconds and an interval
// in milliseconds; those polls send user_code instead of device_code.
type deviceProtocol int

const (
	deviceCodeProtocol deviceProtocol = iota
	legacyUserCodeProtocol
)

func (p deviceProtocol) String() string {
	switch p {
	case deviceCodeProtocol:
		return "device_code"
	case legacyUserCodeProtocol:
		return "legacy_user_code"
	default:
		return fmt.Sprintf("deviceProtocol(%d)", int(p))
	}
}

func detectDeviceProtocol(device deviceAuthorization, now time.Time) deviceProtocol {
	if device.DeviceCode == "" && device.UserCode != "" && device.ExpiredIn > now.UnixMilli() {
		return legacyUserCodeProtocol
	}
	return deviceCodeProtocol
}

// devicePoll is the result of one MiniMax Code device-token poll.
//
//	from           poll result                                       to
//	devicePending  status "pending" or OAuth authorization_pending   devicePending (wait)
//	devicePending  status "slow_down" or OAuth slow_down             deviceSlowDown (interval += 5s, wait)
//	deviceSlowDown (next poll)                                       as devicePending
//	devicePending  status denied/access_denied/expired/expired_token deviceRejected (error)
//	devicePending  any other 2xx body                                deviceAuthorized (save tokens)
//	devicePending  any other error                                   deviceFailed (error)
//	devicePending  device expires_in passes                          expired (error)
type devicePoll int

const (
	devicePending devicePoll = iota
	deviceSlowDown
	deviceRejected
	deviceAuthorized
	deviceFailed
)

func (p devicePoll) String() string {
	switch p {
	case devicePending:
		return "pending"
	case deviceSlowDown:
		return "slow_down"
	case deviceRejected:
		return "rejected"
	case deviceAuthorized:
		return "authorized"
	case deviceFailed:
		return "failed"
	default:
		return fmt.Sprintf("devicePoll(%d)", int(p))
	}
}

// classifyDevicePoll maps one token-endpoint answer. status is the body's
// status field, empty when the poll failed.
func classifyDevicePoll(status string, err error) devicePoll {
	if err == nil {
		switch status {
		case "pending":
			return devicePending
		case "slow_down":
			return deviceSlowDown
		case "denied", "access_denied", "expired", "expired_token":
			return deviceRejected
		default:
			return deviceAuthorized
		}
	}
	switch err {
	case oauthError("authorization_pending"):
		return devicePending
	case oauthError("slow_down"):
		return deviceSlowDown
	default:
		return deviceFailed
	}
}

// checkinDayStatus is the panel's per-day status code, named as the dashboard
// renders it. The upstream owns the transitions; the proxy only observes:
//
//	code  state           ClaimCheckin
//	1     dayUpcoming     ignored
//	2     dayAvailable    makes the panel claimable
//	3     dayClaimed      on today: already claimed (wins over dayAvailable)
//	4     dayUnavailable  ignored
//
// The panel parser accepts only codes 1-4 and at most one dayAvailable day.
type checkinDayStatus int

const (
	dayUpcoming    checkinDayStatus = 1
	dayAvailable   checkinDayStatus = 2
	dayClaimed     checkinDayStatus = 3
	dayUnavailable checkinDayStatus = 4
)

func (s checkinDayStatus) String() string {
	switch s {
	case dayUpcoming:
		return "upcoming"
	case dayAvailable:
		return "available"
	case dayClaimed:
		return "claimed"
	case dayUnavailable:
		return "unavailable"
	default:
		return fmt.Sprintf("checkinDayStatus(%d)", int(s))
	}
}

func (d CheckinDay) dayStatus() checkinDayStatus { return checkinDayStatus(d.Status) }

// checkinEligibility is what a fresh panel allows ClaimCheckin to do. Today's
// dayClaimed takes precedence over any dayAvailable day.
type checkinEligibility int

const (
	checkinClaimable checkinEligibility = iota
	checkinAlreadyClaimed
	checkinUnavailable
)

func (e checkinEligibility) String() string {
	switch e {
	case checkinClaimable:
		return "claimable"
	case checkinAlreadyClaimed:
		return "already_claimed"
	case checkinUnavailable:
		return "unavailable"
	default:
		return fmt.Sprintf("checkinEligibility(%d)", int(e))
	}
}

func (p *CheckinPanel) eligibility() checkinEligibility {
	claimable := false
	for _, day := range p.Days {
		if day.IsToday && day.dayStatus() == dayClaimed {
			return checkinAlreadyClaimed
		}
		claimable = claimable || day.dayStatus() == dayAvailable
	}
	if !claimable {
		return checkinUnavailable
	}
	return checkinClaimable
}

// checkinOutcome is the result category of one automatic check-in pass. The
// String values are persisted in AutoCheckinStatus.LastOutcome and logs.
//
//	claimCheckin result                         outcome          reason
//	nil                                         success          claimed
//	ErrAutoCheckinAlreadyAttempted              already_claimed  already_attempted
//	ErrCheckinAlreadyClaimed                    already_claimed  already_claimed
//	ErrCheckinUnavailable                       unavailable      no_reward_available
//	ErrCheckinInProgress                        skipped          claim_in_progress
//	(no session; claim not attempted)           skipped          signed_out
//	parent context ended                        (not recorded)
//	anything else                               failed           schedulerReason(err)
type checkinOutcome int

const (
	outcomeSuccess checkinOutcome = iota
	outcomeAlreadyClaimed
	outcomeUnavailable
	outcomeSkipped
	outcomeFailed
)

func (o checkinOutcome) String() string {
	switch o {
	case outcomeSuccess:
		return "success"
	case outcomeAlreadyClaimed:
		return "already_claimed"
	case outcomeUnavailable:
		return "unavailable"
	case outcomeSkipped:
		return "skipped"
	case outcomeFailed:
		return "failed"
	default:
		return fmt.Sprintf("checkinOutcome(%d)", int(o))
	}
}
