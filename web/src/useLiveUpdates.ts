import { createContext, createElement, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys, refreshLiveQueries } from './queries.ts'
import { createLiveRefreshScheduler } from './liveRefresh.ts'
import type { InspectedRequest, RequestsResponse } from './api.ts'

const INITIAL_RECONNECT_DELAY_MS = 1_000
const MAX_RECONNECT_DELAY_MS = 15_000
// WebSockets need a connection the server can hijack; some paths (HTTP/2
// through a proxy) cannot, and the dial fails with 501 forever. After this
// many failed dials the hook switches to the SSE twin endpoint, which works
// over every transport.
const WS_ATTEMPTS_BEFORE_SSE = 2

// How many live-injected requests to keep in the recent-requests cache before
// the next refetch reconciles it with the server's retained history.
const LIVE_CACHE_CAP = 50

export interface LiveRequest {
  count: number
  request: InspectedRequest
  receivedAt: number
}

interface LiveStatsState {
  connected: boolean
  live: LiveRequest | null
}

// Prepend a live request to the cached recent-requests list: newest first,
// deduped by id, capped so a burst cannot grow the list past what the next
// refetch will reconcile.
export function injectLiveRequestIntoCache(request: InspectedRequest, old?: RequestsResponse): RequestsResponse {
  const rest = (old?.requests ?? []).filter((entry) => entry.id !== request.id)
  return { requests: [request, ...rest].slice(0, LIVE_CACHE_CAP) }
}

const INITIAL_STATE: LiveStatsState = { connected: false, live: null }

const LiveStatsContext = createContext<LiveStatsState>(INITIAL_STATE)

// Consumers read connection status plus the latest live request. The root
// provider owns the single connection, independent of route, layout, or the
// number of visible status badges.
export function useLiveStatsUpdates() {
  return useContext(LiveStatsContext)
}

export function LiveStatsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [connected, setConnected] = useState(false)
  const [live, setLive] = useState<LiveRequest | null>(null)

  useEffect(() => {
    let socket: WebSocket | undefined
    let eventSource: EventSource | undefined
    let reconnectTimer: number | undefined
    let retryDelay = INITIAL_RECONNECT_DELAY_MS
    let wsFailures = 0
    let stopped = false
    let requestCount = 0

    const refresh = createLiveRefreshScheduler(() => { void refreshLiveQueries(queryClient) })

    // Inject the completed request into the recent-requests cache so the
    // activity feed shows it the moment the event lands; the scheduled refetch
    // then reconciles against the server's retained history.
    const injectLiveRequest = (request: InspectedRequest) => {
      queryClient.setQueryData<RequestsResponse>(queryKeys.requests, (old) => injectLiveRequestIntoCache(request, old))
    }

    const onEvent = (data: unknown) => {
      if (stopped || typeof data !== 'string') return
      try {
        const parsed = JSON.parse(data)
        if (parsed?.type === 'request') {
          requestCount += 1
          const liveRequest: LiveRequest = { count: requestCount, request: parsed.request, receivedAt: Date.now() }
          setLive(liveRequest)
          injectLiveRequest(parsed.request)
          refresh.schedule()
        } else if (parsed?.type === 'stats-updated') {
          refresh.schedule()
        }
      } catch {
        // not JSON, ignore
      }
    }

    const connectSSE = () => {
      eventSource = new EventSource('/api/updates/sse')
      eventSource.addEventListener('open', () => {
        if (stopped) return
        retryDelay = INITIAL_RECONNECT_DELAY_MS
        setConnected(true)
        refresh.schedule()
      })
      eventSource.addEventListener('message', (event) => onEvent(event.data))
      eventSource.addEventListener('error', () => {
        if (stopped) return
        setConnected(false)
        // EventSource reconnects on its own; only surface the state.
      })
    }

    const connect = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/updates/ws`)

      socket.addEventListener('open', () => {
        if (stopped) return
        refresh.schedule()
        retryDelay = INITIAL_RECONNECT_DELAY_MS
        wsFailures = 0
        setConnected(true)
      })

      socket.addEventListener('message', (event) => onEvent(event.data))

      socket.addEventListener('close', () => {
        if (stopped) return
        setConnected(false)
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

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        refresh.flush()
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange)

    connect()

    return () => {
      stopped = true
      window.clearTimeout(reconnectTimer)
      refresh.cancel()
      document.removeEventListener('visibilitychange', onVisibilityChange)
      socket?.close()
      eventSource?.close()
    }
  }, [queryClient])

  return createElement(LiveStatsContext.Provider, { value: { connected, live } }, children)
}
