// Coalesce bursts in a fixed window. Later events never postpone the deadline,
// so continuous traffic still refreshes the dashboard promptly: the window is
// short enough that a first request updates the feed near-instantly, while a
// traffic burst still produces one refetch per window rather than per request.
//
// Two states:
//
//   idle       schedule → scheduled (start the window timer)
//   idle       flush    → idle      (refresh now)
//   idle       cancel   → idle
//   scheduled  schedule → scheduled (no-op: the deadline is fixed)
//   scheduled  timer    → idle      (refresh)
//   scheduled  flush    → idle      (drop the timer, refresh now)
//   scheduled  cancel   → idle      (drop the timer)
type RefreshWindow =
  | { kind: 'idle' }
  | { kind: 'scheduled'; timer: ReturnType<typeof setTimeout> }

export function createLiveRefreshScheduler(refresh: () => void, delay = 150) {
  let current: RefreshWindow = { kind: 'idle' }
  const cancel = () => {
    if (current.kind === 'scheduled') clearTimeout(current.timer)
    current = { kind: 'idle' }
  }
  return {
    schedule() {
      if (current.kind === 'scheduled') return
      current = {
        kind: 'scheduled',
        timer: setTimeout(() => {
          current = { kind: 'idle' }
          refresh()
        }, delay),
      }
    },
    flush() {
      cancel()
      refresh()
    },
    cancel,
  }
}
