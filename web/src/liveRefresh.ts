// Coalesce bursts in a fixed window. Later events never postpone the deadline,
// so continuous traffic still refreshes the dashboard at most twice a second.
export function createLiveRefreshScheduler(refresh: () => void, delay = 500) {
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
