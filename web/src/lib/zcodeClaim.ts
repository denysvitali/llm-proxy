import type { ZcodeClaimResult } from '../api.ts'

// ZCode offer-claim state machine. Pure: ZcodeOffers drives it with
// useReducer and keeps the network calls outside.
//
// Each plan ID has at most one Attempt; a plan with no entry has never been
// tried in this session ("none"). Settled results are classified once, when
// they arrive:
//
//   complete         ok, or already_claimed
//   retryable        captcha / login_required / invalid_request
//   risk-blocked     risk_blocked: latches every claim off for the session
//   rejected         any other failure: terminal for this plan
//   unknown-outcome  the request itself failed; the plan may have been claimed
//
// Every attempt also carries `reviewed`: whether a full offers+quota refresh
// has succeeded since it was recorded. Only a reviewed, retryable attempt may
// be cleared to allow another claim.
//
// Transition table for one plan (anything else is a no-op):
//
//   none             submit             → in-flight
//   in-flight        claim-succeeded    → complete | retryable | risk-blocked | rejected
//   in-flight        claim-failed       → unknown-outcome
//   any attempt      review-succeeded   → same kind, reviewed
//   retryable+rev.   retry-enabled      → none
//
// The page-level review status is a separate three-state union:
//
//   idle | failed    review-started     → reviewing
//   reviewing        review-succeeded   → idle   (marks every attempt reviewed)
//   reviewing        review-failed      → failed(message)
//
// `submit` is only accepted for a non-blank plan ID with no attempt while the
// session is not risk-blocked; callers additionally gate on the global
// pending-mutation count and their own synchronous in-flight latch.

export type ClaimOutcomeKind = 'complete' | 'retryable' | 'risk-blocked' | 'rejected'

export type ClaimAttempt =
  | { kind: 'in-flight'; reviewed: boolean }
  | { kind: ClaimOutcomeKind; result: ZcodeClaimResult; reviewed: boolean }
  | { kind: 'unknown-outcome'; error: string; reviewed: boolean }

export type ClaimReview = { kind: 'idle' } | { kind: 'reviewing' } | { kind: 'failed'; message: string }

export interface ClaimState {
  attempts: Record<string, ClaimAttempt>
  review: ClaimReview
}

export type ClaimEvent =
  | { type: 'submit'; planID: string }
  | { type: 'claim-succeeded'; planID: string; result: ZcodeClaimResult }
  | { type: 'claim-failed'; planID: string; message: string }
  | { type: 'review-started' }
  | { type: 'review-succeeded' }
  | { type: 'review-failed'; message: string }
  | { type: 'retry-enabled'; planID: string }

export const INITIAL_CLAIM_STATE: ClaimState = { attempts: {}, review: { kind: 'idle' } }

const RETRYABLE_FAILURES = ['captcha', 'login_required', 'invalid_request']

export function classifyClaimResult(result: ZcodeClaimResult): ClaimOutcomeKind {
  if (result.ok || result.failure_kind === 'already_claimed') return 'complete'
  const kind = result.failure_kind ?? ''
  if (RETRYABLE_FAILURES.includes(kind)) return 'retryable'
  if (kind === 'risk_blocked') return 'risk-blocked'
  return 'rejected'
}

// The session latch: any recorded result reporting risk_blocked disables all
// further claims until the page reloads.
export function isRiskBlocked(attempts: Record<string, ClaimAttempt>): boolean {
  return Object.values(attempts).some((attempt) => 'result' in attempt && attempt.result.failure_kind === 'risk_blocked')
}

export function canSubmitClaim(state: ClaimState, planID: string): boolean {
  return !isRiskBlocked(state.attempts) && !state.attempts[planID] && planID.trim() !== ''
}

function setAttempt(state: ClaimState, planID: string, attempt: ClaimAttempt): ClaimState {
  return { ...state, attempts: { ...state.attempts, [planID]: attempt } }
}

export function claimReducer(state: ClaimState, event: ClaimEvent): ClaimState {
  switch (event.type) {
    case 'submit':
      return canSubmitClaim(state, event.planID) ? setAttempt(state, event.planID, { kind: 'in-flight', reviewed: false }) : state
    // Settlement always records the outcome as a fresh, unreviewed attempt.
    case 'claim-succeeded':
      return setAttempt(state, event.planID, { kind: classifyClaimResult(event.result), result: event.result, reviewed: false })
    case 'claim-failed':
      return setAttempt(state, event.planID, { kind: 'unknown-outcome', error: event.message, reviewed: false })
    case 'review-started':
      return { ...state, review: { kind: 'reviewing' } }
    case 'review-succeeded':
      return {
        review: { kind: 'idle' },
        attempts: Object.fromEntries(Object.entries(state.attempts).map(([id, attempt]) => [id, { ...attempt, reviewed: true }])),
      }
    case 'review-failed':
      return { ...state, review: { kind: 'failed', message: event.message } }
    case 'retry-enabled': {
      const attempt = state.attempts[event.planID]
      if (attempt?.kind !== 'retryable' || !attempt.reviewed) return state
      const attempts = { ...state.attempts }
      delete attempts[event.planID]
      return { ...state, attempts }
    }
  }
}
