// Setup page's browser-reachability probe.
//
//   idle | ok | failed   start    → testing  (the previous result is cleared)
//   testing              reached  → ok | failed (by HTTP status)
//   testing              error    → failed   (network error or timeout)
//
// The button is disabled while testing, so probes never overlap; a settling
// probe simply records its result.
export type ConnectionProbe =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'ok'; message: string }
  | { kind: 'failed'; message: string }

export type ConnectionProbeEvent =
  | { type: 'start' }
  | { type: 'reached'; ok: boolean; status: number }
  | { type: 'error' }

export const INITIAL_CONNECTION_PROBE: ConnectionProbe = { kind: 'idle' }

export function connectionProbeReducer(_state: ConnectionProbe, event: ConnectionProbeEvent): ConnectionProbe {
  switch (event.type) {
    case 'start':
      return { kind: 'testing' }
    case 'reached':
      return event.ok
        ? { kind: 'ok', message: 'Gateway is reachable.' }
        : { kind: 'failed', message: `Gateway returned HTTP ${event.status}.` }
    case 'error':
      return { kind: 'failed', message: 'Could not reach the gateway. Check your connection and try again.' }
  }
}
