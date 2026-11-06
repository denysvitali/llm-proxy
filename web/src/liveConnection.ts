// Live-update connection state machine. Pure: no timers, sockets, or React.
// LiveStatsProvider owns the resources and executes the returned commands.
//
// WebSockets need a connection the server can hijack; some paths (HTTP/2
// through a proxy) cannot, and the dial fails with 501 forever. After
// WS_ATTEMPTS_BEFORE_SSE failed dials in a row the machine switches to the SSE
// twin endpoint, which works over every transport. SSE is terminal: the
// browser's EventSource reconnects on its own, so the machine only mirrors its
// open/error status.
//
// Transition table (event → next state, commands). Rows not listed leave the
// state unchanged and emit nothing.
//
//   idle            start      → connecting-ws(failures 0, delay 1s)   open-ws
//   connecting-ws   ws-open    → open-ws                               refresh, connected=true
//   connecting-ws   ws-close   → failures+1 > limit ? sse(closed)      connected=false, open-sse
//                                                   : backoff(wait delay, delay×2 capped)
//                                                                     connected=false, schedule-reconnect(delay)
//   open-ws         ws-close   → backoff(failures 1, wait 1s, delay 2s) connected=false, schedule-reconnect(1s)
//   backoff         retry      → connecting-ws(same failures, delay)    open-ws
//   sse(any)        sse-open   → sse(open)                             connected=true, refresh
//   sse(any)        sse-error  → sse(closed)                           connected=false
//   sse(any)        ws-close   → sse (unchanged)                       connected=false
//   any but stopped stop       → stopped                               (owner tears down)
//   stopped         *          → stopped                               (nothing; late events are ignored)
//
// Opening a WebSocket resets the failure count and the backoff delay, so a
// socket that opens and then closes again keeps retrying WebSocket forever
// instead of reaching the SSE fallback. That matches the historical behavior
// and is kept as-is.

export const INITIAL_RECONNECT_DELAY_MS = 1_000
export const MAX_RECONNECT_DELAY_MS = 15_000
export const WS_ATTEMPTS_BEFORE_SSE = 2

export type LiveConnection =
  | { kind: 'idle' }
  // A WebSocket dial is outstanding. `failures` counts consecutive failed
  // dials; `retryDelay` is the wait to use if this dial fails too.
  | { kind: 'connecting-ws'; failures: number; retryDelay: number }
  | { kind: 'open-ws' }
  // Waiting `waitMs` before the next dial; `retryDelay` is the following wait.
  | { kind: 'backoff'; failures: number; waitMs: number; retryDelay: number }
  // Terminal transport. `open` mirrors the EventSource's last open/error event.
  | { kind: 'sse'; open: boolean }
  | { kind: 'stopped' }

export type LiveConnectionEvent =
  | { type: 'start' }
  | { type: 'ws-open' }
  | { type: 'ws-close' }
  | { type: 'retry' }
  | { type: 'sse-open' }
  | { type: 'sse-error' }
  | { type: 'stop' }

export type LiveConnectionCommand =
  | { type: 'open-ws' }
  | { type: 'open-sse' }
  | { type: 'schedule-reconnect'; delayMs: number }
  | { type: 'set-connected'; connected: boolean }
  | { type: 'refresh' }

export interface LiveConnectionStep {
  state: LiveConnection
  commands: LiveConnectionCommand[]
}

export const INITIAL_LIVE_CONNECTION: LiveConnection = { kind: 'idle' }

const stay = (state: LiveConnection, commands: LiveConnectionCommand[] = []): LiveConnectionStep => ({ state, commands })

function failedDial(failures: number, retryDelay: number): LiveConnectionStep {
  const next = failures + 1
  if (next > WS_ATTEMPTS_BEFORE_SSE) {
    return { state: { kind: 'sse', open: false }, commands: [{ type: 'set-connected', connected: false }, { type: 'open-sse' }] }
  }
  return {
    state: { kind: 'backoff', failures: next, waitMs: retryDelay, retryDelay: Math.min(retryDelay * 2, MAX_RECONNECT_DELAY_MS) },
    commands: [{ type: 'set-connected', connected: false }, { type: 'schedule-reconnect', delayMs: retryDelay }],
  }
}

export function transitionLiveConnection(state: LiveConnection, event: LiveConnectionEvent): LiveConnectionStep {
  if (state.kind === 'stopped') return stay(state)
  if (event.type === 'stop') return stay({ kind: 'stopped' })
  switch (state.kind) {
    case 'idle':
      return event.type === 'start'
        ? { state: { kind: 'connecting-ws', failures: 0, retryDelay: INITIAL_RECONNECT_DELAY_MS }, commands: [{ type: 'open-ws' }] }
        : stay(state)
    case 'connecting-ws':
      if (event.type === 'ws-open') return { state: { kind: 'open-ws' }, commands: [{ type: 'refresh' }, { type: 'set-connected', connected: true }] }
      if (event.type === 'ws-close') return failedDial(state.failures, state.retryDelay)
      return stay(state)
    case 'open-ws':
      return event.type === 'ws-close' ? failedDial(0, INITIAL_RECONNECT_DELAY_MS) : stay(state)
    case 'backoff':
      return event.type === 'retry'
        ? { state: { kind: 'connecting-ws', failures: state.failures, retryDelay: state.retryDelay }, commands: [{ type: 'open-ws' }] }
        : stay(state)
    case 'sse':
      if (event.type === 'sse-open') return { state: { kind: 'sse', open: true }, commands: [{ type: 'set-connected', connected: true }, { type: 'refresh' }] }
      if (event.type === 'sse-error') return { state: { kind: 'sse', open: false }, commands: [{ type: 'set-connected', connected: false }] }
      if (event.type === 'ws-close') return stay(state, [{ type: 'set-connected', connected: false }])
      return stay(state)
  }
}
