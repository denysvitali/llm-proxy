import type { MiniMaxCheckinDay, MiniMaxCheckinPanel, MiniMaxCheckinResult } from '../api.ts'

// MiniMax daily check-in states, named instead of the upstream status codes.
//
// Day status codes (anything unrecognised reads as upcoming):
//   1 → upcoming   2 → available   3 → claimed   4 → unavailable
export type CheckinDayState = 'upcoming' | 'available' | 'claimed' | 'unavailable'

export function checkinDayState(day: Pick<MiniMaxCheckinDay, 'status'>): CheckinDayState {
  switch (day.status) {
    case 2: return 'available'
    case 3: return 'claimed'
    case 4: return 'unavailable'
    default: return 'upcoming'
  }
}

export const CHECKIN_DAY_LABEL: Record<CheckinDayState, string> = {
  upcoming: 'Upcoming', available: 'Available', claimed: 'Claimed', unavailable: 'Unavailable',
}

// Whole-panel state, first match wins:
//   claimed      today's day is claimed
//   claimable    some day is available (and today is not yet claimed)
//   unavailable  the schedule has days but none can be claimed
//   no-schedule  no panel, or a panel without days
export type CheckinPanelState = 'claimed' | 'claimable' | 'unavailable' | 'no-schedule'

export function checkinPanelState(panel?: MiniMaxCheckinPanel): CheckinPanelState {
  const days = panel?.days ?? []
  if (days.some((day) => day.is_today && checkinDayState(day) === 'claimed')) return 'claimed'
  if (days.some((day) => checkinDayState(day) === 'available')) return 'claimable'
  return days.length ? 'unavailable' : 'no-schedule'
}

// What a successful claim reported. claim_result 2 means today was already
// claimed; anything else is a fresh claim, whose expiry is reported only for
// claim_result 1.
export type CheckinOutcome =
  | { kind: 'already-checked-in' }
  | { kind: 'checked-in'; points: number; expireAtMs?: number }

export function checkinOutcome(result: MiniMaxCheckinResult): CheckinOutcome {
  if (result.claim_result === 2) return { kind: 'already-checked-in' }
  return { kind: 'checked-in', points: result.points, expireAtMs: result.claim_result === 1 ? result.expire_at_ms : undefined }
}
