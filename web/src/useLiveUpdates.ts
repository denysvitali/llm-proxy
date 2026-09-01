import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

const INITIAL_RECONNECT_DELAY_MS = 1_000
const MAX_RECONNECT_DELAY_MS = 15_000
// WebSockets need a connection the server can hijack; some paths (HTTP/2
// through a proxy) cannot, and the dial fails with 501 forever. After this
// many failed dials the hook switches to the SSE twin endpoint, which works
// over every transport.
const WS_ATTEMPTS_BEFORE_SSE = 2

export function useLiveStatsUpdates() {
  const queryClient = useQueryClient()
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    let socket: WebSocket | undefined
    let eventSource: EventSource | undefined
    let reconnectTimer: number | undefined
    let retryDelay = INITIAL_RECONNECT_DELAY_MS
    let wsFailures = 0
    let stopped = false

    // Every completed upstream request triggers a stats-updated event. On a
    // busy proxy those arrive several times a second; refetching on each one
    // makes the dashboard visibly pulse. Coalesce bursts into one refetch.
    let invalidateTimer: number | undefined
    const scheduleInvalidate = () => {
      window.clearTimeout(invalidateTimer)
      invalidateTimer = window.setTimeout(() => {
        invalidateTimer = undefined
        void queryClient.invalidateQueries({ queryKey: ['stats'] })
        void queryClient.invalidateQueries({ queryKey: ['stats-series'] })
        void queryClient.invalidateQueries({ queryKey: ['overview'] })
      }, 500)
    }

    const onEvent = (data: unknown) => {
      if ((data as string).startsWith('{"type":"stats-updated"}')) {
        scheduleInvalidate()
      }
    }

    const connectSSE = () => {
      eventSource = new EventSource('/api/updates/sse')
      eventSource.addEventListener('open', () => {
        retryDelay = INITIAL_RECONNECT_DELAY_MS
        setConnected(true)
      })
      eventSource.addEventListener('message', (event) => onEvent(event.data))
      eventSource.addEventListener('error', () => {
        setConnected(false)
        // EventSource reconnects on its own; only surface the state.
      })
    }

    const connect = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/updates/ws`)

      socket.addEventListener('open', () => {
        retryDelay = INITIAL_RECONNECT_DELAY_MS
        wsFailures = 0
        setConnected(true)
      })

      socket.addEventListener('message', (event) => onEvent(event.data))

      socket.addEventListener('close', () => {
        setConnected(false)
        if (stopped) return
        if (eventSource) return
        wsFailures += 1
        if (wsFailures > WS_ATTEMPTS_BEFORE_SSE) {
          connectSSE()
          return
        }
        reconnectTimer = window.setTimeout(connect, retryDelay)
        retryDelay = Math.min(retryDelay * 2, MAX_RECONNECT_DELAY_MS)
      })
    }

    connect()

    return () => {
      stopped = true
      window.clearTimeout(reconnectTimer)
      window.clearTimeout(invalidateTimer)
      socket?.close()
      eventSource?.close()
    }
  }, [queryClient])

  return connected
}
