import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  INITIAL_LIVE_CONNECTION, INITIAL_RECONNECT_DELAY_MS, MAX_RECONNECT_DELAY_MS, transitionLiveConnection,
} from '../src/liveConnection.ts'
import { createLiveRefreshScheduler } from '../src/liveRefresh.ts'
import { canSubmitClaim, claimReducer, classifyClaimResult, INITIAL_CLAIM_STATE, isRiskBlocked } from '../src/lib/zcodeClaim.ts'
import { checkinDayState, checkinOutcome, checkinPanelState } from '../src/lib/minimaxCheckin.ts'
import { connectionProbeReducer, INITIAL_CONNECTION_PROBE } from '../src/lib/connectionProbe.ts'
import { healthState, uptimeReading } from '../src/lib/stats.ts'
import { statsStateOf } from '../src/components/catalog/providerData.ts'
import { chordStep, IDLE_CHORD } from '../src/lib/keyChord.ts'
import { INITIAL_PALETTE, paletteReducer } from '../src/lib/paletteCursor.ts'
import { countUpFrame, countUpPhase } from '../src/lib/countUp.ts'
import { pageWindow } from '../src/lib/pagination.ts'

function run(events, state = INITIAL_LIVE_CONNECTION) {
  const commands = []
  for (const type of events) {
    const step = transitionLiveConnection(state, { type })
    state = step.state
    commands.push(...step.commands)
  }
  return { state, commands }
}

test('live connection backs off twice, then falls back to terminal SSE', () => {
  let r = run(['start'])
  assert.deepEqual(r.state, { kind: 'connecting-ws', failures: 0, retryDelay: INITIAL_RECONNECT_DELAY_MS })
  assert.deepEqual(r.commands, [{ type: 'open-ws' }])
  r = run(['ws-close'], r.state)
  assert.deepEqual(r.state, { kind: 'backoff', failures: 1, waitMs: 1000, retryDelay: 2000 })
  assert.deepEqual(r.commands, [{ type: 'set-connected', connected: false }, { type: 'schedule-reconnect', delayMs: 1000 }])
  r = run(['retry', 'ws-close'], r.state)
  assert.deepEqual(r.state, { kind: 'backoff', failures: 2, waitMs: 2000, retryDelay: 4000 })
  r = run(['retry', 'ws-close'], r.state)
  assert.deepEqual(r.state, { kind: 'sse', open: false })
  assert.deepEqual(r.commands.slice(-2), [{ type: 'set-connected', connected: false }, { type: 'open-sse' }])
  r = run(['sse-open'], r.state)
  assert.deepEqual(r.state, { kind: 'sse', open: true })
  assert.deepEqual(r.commands, [{ type: 'set-connected', connected: true }, { type: 'refresh' }])
  r = run(['sse-error'], r.state)
  assert.deepEqual(r, { state: { kind: 'sse', open: false }, commands: [{ type: 'set-connected', connected: false }] })
})

test('an opened WebSocket resets failures, so open-then-close loops never reach SSE', () => {
  let r = run(['start', 'ws-close', 'retry', 'ws-close', 'retry', 'ws-open'])
  assert.deepEqual(r.state, { kind: 'open-ws' })
  assert.deepEqual(r.commands.slice(-2), [{ type: 'refresh' }, { type: 'set-connected', connected: true }])
  for (let i = 0; i < 5; i++) r = run(['ws-close', 'retry', 'ws-open'], r.state)
  assert.equal(r.state.kind, 'open-ws')
  assert.ok(!r.commands.some((command) => command.type === 'open-sse'))
  r = run(['ws-close'], r.state)
  assert.deepEqual(r.state, { kind: 'backoff', failures: 1, waitMs: 1000, retryDelay: 2000 })
})

test('backoff delay is capped and stopped ignores every event', () => {
  const step = transitionLiveConnection({ kind: 'connecting-ws', failures: 0, retryDelay: 12_000 }, { type: 'ws-close' })
  assert.equal(step.state.retryDelay, MAX_RECONNECT_DELAY_MS)
  const stopped = run(['start', 'stop'])
  assert.deepEqual(stopped.state, { kind: 'stopped' })
  for (const type of ['start', 'ws-open', 'ws-close', 'retry', 'sse-open', 'sse-error', 'stop']) {
    assert.deepEqual(transitionLiveConnection(stopped.state, { type }), { state: { kind: 'stopped' }, commands: [] })
  }
})

test('refresh coalescer keeps a fixed deadline and flush/cancel return it to idle', async () => {
  let calls = 0
  const scheduler = createLiveRefreshScheduler(() => { calls += 1 }, 5)
  scheduler.schedule()
  scheduler.schedule()
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(calls, 1)
  scheduler.schedule()
  scheduler.flush()
  assert.equal(calls, 2)
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(calls, 2)
  scheduler.schedule()
  scheduler.cancel()
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(calls, 2)
})

test('zcode claim results classify into explicit outcomes', () => {
  assert.equal(classifyClaimResult({ ok: true, plan_id: 'p' }), 'complete')
  assert.equal(classifyClaimResult({ ok: false, plan_id: 'p', failure_kind: 'already_claimed' }), 'complete')
  for (const kind of ['captcha', 'login_required', 'invalid_request']) {
    assert.equal(classifyClaimResult({ ok: false, plan_id: 'p', failure_kind: kind }), 'retryable')
  }
  assert.equal(classifyClaimResult({ ok: false, plan_id: 'p', failure_kind: 'risk_blocked' }), 'risk-blocked')
  assert.equal(classifyClaimResult({ ok: false, plan_id: 'p' }), 'rejected')
})

test('zcode claim flow: submit, settle, review, re-enable retry', () => {
  let state = claimReducer(INITIAL_CLAIM_STATE, { type: 'submit', planID: 'a' })
  assert.deepEqual(state.attempts.a, { kind: 'in-flight', reviewed: false })
  assert.equal(canSubmitClaim(state, 'a'), false)
  assert.equal(canSubmitClaim(state, '  '), false)
  const captcha = { ok: false, plan_id: 'a', failure_kind: 'captcha' }
  state = claimReducer(state, { type: 'claim-succeeded', planID: 'a', result: captcha })
  assert.equal(state.attempts.a.kind, 'retryable')
  // Not yet reviewed: re-enabling is refused.
  assert.equal(claimReducer(state, { type: 'retry-enabled', planID: 'a' }), state)
  state = claimReducer(state, { type: 'review-started' })
  assert.deepEqual(state.review, { kind: 'reviewing' })
  state = claimReducer(state, { type: 'review-succeeded' })
  assert.deepEqual(state.review, { kind: 'idle' })
  assert.equal(state.attempts.a.reviewed, true)
  state = claimReducer(state, { type: 'retry-enabled', planID: 'a' })
  assert.deepEqual(state.attempts, {})
  state = claimReducer(state, { type: 'submit', planID: 'a' })
  state = claimReducer(state, { type: 'claim-failed', planID: 'a', message: 'network' })
  assert.deepEqual(state.attempts.a, { kind: 'unknown-outcome', error: 'network', reviewed: false })
  state = claimReducer(state, { type: 'review-started' })
  state = claimReducer(state, { type: 'review-failed', message: 'nope' })
  assert.deepEqual(state.review, { kind: 'failed', message: 'nope' })
  assert.equal(state.attempts.a.reviewed, false)
})

test('a risk-blocked result latches every claim off', () => {
  let state = claimReducer(INITIAL_CLAIM_STATE, { type: 'submit', planID: 'a' })
  state = claimReducer(state, { type: 'claim-succeeded', planID: 'a', result: { ok: false, plan_id: 'a', failure_kind: 'risk_blocked' } })
  assert.equal(isRiskBlocked(state.attempts), true)
  assert.equal(canSubmitClaim(state, 'b'), false)
  assert.equal(claimReducer(state, { type: 'submit', planID: 'b' }), state)
})

test('minimax check-in status codes map to named states', () => {
  assert.deepEqual([1, 2, 3, 4, 9].map((status) => checkinDayState({ status })), ['upcoming', 'available', 'claimed', 'unavailable', 'upcoming'])
  const day = (status, is_today = false) => ({ day_no: 1, points: 1, status, is_today })
  assert.equal(checkinPanelState(undefined), 'no-schedule')
  assert.equal(checkinPanelState({ scene: 1, days: [] }), 'no-schedule')
  assert.equal(checkinPanelState({ scene: 1, days: [day(3, true), day(2)] }), 'claimed')
  assert.equal(checkinPanelState({ scene: 1, days: [day(3), day(2)] }), 'claimable')
  assert.equal(checkinPanelState({ scene: 1, days: [day(1, true), day(4)] }), 'unavailable')
  assert.deepEqual(checkinOutcome({ claim_result: 2, points: 5, expire_at_ms: 7 }), { kind: 'already-checked-in' })
  assert.deepEqual(checkinOutcome({ claim_result: 1, points: 5, expire_at_ms: 7 }), { kind: 'checked-in', points: 5, expireAtMs: 7 })
})

test('setup connection probe moves idle → testing → ok/failed and restarts cleanly', () => {
  let probe = connectionProbeReducer(INITIAL_CONNECTION_PROBE, { type: 'start' })
  assert.deepEqual(probe, { kind: 'testing' })
  probe = connectionProbeReducer(probe, { type: 'reached', ok: true, status: 200 })
  assert.deepEqual(probe, { kind: 'ok', message: 'Gateway is reachable.' })
  probe = connectionProbeReducer(probe, { type: 'start' })
  assert.deepEqual(probe, { kind: 'testing' })
  assert.deepEqual(connectionProbeReducer(probe, { type: 'reached', ok: false, status: 503 }), { kind: 'failed', message: 'Gateway returned HTTP 503.' })
  assert.equal(connectionProbeReducer(probe, { type: 'error' }).kind, 'failed')
})

test('uptime readings separate no traffic, unavailable sentinels, and measured tones', () => {
  assert.deepEqual(uptimeReading(NaN, 0), { kind: 'no-traffic' })
  assert.deepEqual(uptimeReading(NaN, NaN), { kind: 'unavailable' })
  assert.deepEqual(uptimeReading(1.2, 5), { kind: 'unavailable' })
  assert.deepEqual(uptimeReading(0.5, -1), { kind: 'unavailable' })
  assert.deepEqual(uptimeReading(0.995, 10), { kind: 'measured', tone: 'good', uptime: 0.995, requests: 10 })
  assert.equal(uptimeReading(0.95, 10).tone, 'warning')
  assert.equal(uptimeReading(0.5, 10).tone, 'critical')
  assert.equal(healthState(0, 0), 'no-traffic')
  assert.equal(statsStateOf({ data: {}, isPending: false }), 'ready')
  assert.equal(statsStateOf({ data: undefined, isPending: true }), 'loading')
  assert.equal(statsStateOf({ data: undefined, isPending: false }), 'unavailable')
})

test('keyboard chord: g then target within the window navigates once', () => {
  const key = (k, now, extra = {}) => ({ key: k, metaKey: false, ctrlKey: false, blocked: false, now, ...extra })
  let step = chordStep(IDLE_CHORD, key('g', 1000))
  assert.deepEqual(step.state, { kind: 'awaiting-target', since: 1000 })
  step = chordStep(step.state, key('m', 1500))
  assert.deepEqual(step, { state: IDLE_CHORD, action: { type: 'navigate', path: '/models' }, preventDefault: false })
  assert.deepEqual(chordStep(chordStep(IDLE_CHORD, key('g', 0)).state, key('o', 1000)).action, { type: 'none' })
  assert.deepEqual(chordStep(IDLE_CHORD, key('o', 0)).action, { type: 'none' })
  assert.deepEqual(chordStep(chordStep(IDLE_CHORD, key('g', 0)).state, key('x', 1)).action, { type: 'none' })
  assert.deepEqual(chordStep({ kind: 'awaiting-target', since: 0 }, key('s', 1, { blocked: true })), { state: IDLE_CHORD, action: { type: 'none' }, preventDefault: false })
  assert.deepEqual(chordStep({ kind: 'awaiting-target', since: 0 }, key('p', 1, { ctrlKey: true })).action, { type: 'none' })
  assert.deepEqual(chordStep(IDLE_CHORD, key('K', 0, { metaKey: true })), { state: IDLE_CHORD, action: { type: 'open-palette' }, preventDefault: true })
  assert.deepEqual(chordStep(IDLE_CHORD, key('?', 0)), { state: IDLE_CHORD, action: { type: 'open-shortcuts' }, preventDefault: true })
})

test('palette cursor clamps and resets with the query', () => {
  let state = paletteReducer(INITIAL_PALETTE, { type: 'move-down', count: 3 })
  state = paletteReducer(state, { type: 'move-down', count: 3 })
  state = paletteReducer(state, { type: 'move-down', count: 3 })
  assert.equal(state.cursor, 2)
  state = paletteReducer(state, { type: 'query-changed', query: 'zz' })
  assert.deepEqual(state, { query: 'zz', cursor: 0 })
  state = paletteReducer(state, { type: 'move-down', count: 0 })
  assert.equal(state.cursor, -1)
  state = paletteReducer(state, { type: 'move-up' })
  assert.equal(state.cursor, 0)
  assert.deepEqual(paletteReducer({ query: 'a', cursor: 4 }, { type: 'reset' }), INITIAL_PALETTE)
  assert.equal(paletteReducer(INITIAL_PALETTE, { type: 'hover', index: 2 }).cursor, 2)
})

test('count-up counts finite numbers only and lands exactly on target', () => {
  assert.deepEqual(countUpPhase(42, true), { kind: 'counting', target: 42 })
  assert.deepEqual(countUpPhase(42, false), { kind: 'static' })
  assert.deepEqual(countUpPhase('34.9K', true), { kind: 'static' })
  assert.deepEqual(countUpPhase(NaN, true), { kind: 'static' })
  assert.deepEqual(countUpFrame(100, 0), { shown: 0, done: false })
  assert.equal(countUpFrame(100, 320).done, false)
  assert.deepEqual(countUpFrame(100, 10_000), { shown: 100, done: true })
})

test('page window clamps a stale page to the last page', () => {
  assert.deepEqual(pageWindow(40, 18, 0), { currentPage: 0, lastPage: 2, start: 1, end: 18 })
  assert.deepEqual(pageWindow(40, 18, 9), { currentPage: 2, lastPage: 2, start: 37, end: 40 })
  assert.deepEqual(pageWindow(0, 18, 3), { currentPage: 0, lastPage: 0, start: 1, end: 0 })
})

test('request inspection view distinguishes expired from failed', async () => {
  const { ApiError } = await import('../src/api.ts')
  const { requestInspectView } = await import('../src/lib/requestInspect.ts')
  const base = { isPending: false, isError: false, error: null, data: undefined }
  assert.deepEqual(requestInspectView({ ...base, isPending: true }), { kind: 'loading' })
  assert.deepEqual(requestInspectView({ ...base, isError: true, error: new ApiError('gone', 404) }), { kind: 'expired' })
  assert.deepEqual(requestInspectView({ ...base, isError: true, error: new Error('x') }), { kind: 'failed' })
  assert.deepEqual(requestInspectView({ ...base, data: { id: 'r' } }), { kind: 'loaded', request: { id: 'r' } })
  assert.deepEqual(requestInspectView(base), { kind: 'empty' })
})
