// Coalesce bursts in a fixed window. Later events never postpone the deadline,
// so continuous traffic still refreshes the dashboard promptly: the window is
// short enough that a first request updates the feed near-instantly, while a
// traffic burst still produces one refetch per window rather than per request.
export function createLiveRefreshScheduler(refresh: () => void, delay = 150) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const cancel = () => {
    clearTimeout(timer)
    timer = undefined
  }
  return {
    schedule() {
      if (timer !== undefined) return
      timer = setTimeout(() => {
        timer = undefined
        refresh()
      }, delay)
    },
    flush() {
      cancel()
      refresh()
    },
    cancel,
  }
}
