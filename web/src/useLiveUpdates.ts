import { createContext, createElement, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys, refreshLiveQueries } from './queries.ts'
import { createLiveRefreshScheduler } from './liveRefresh.ts'
import type { InspectedRequest, RequestsResponse } from './api.ts'
import {
  INITIAL_LIVE_CONNECTION, transitionLiveConnection, type LiveConnection, type LiveConnectionEvent,
} from './liveConnection.ts'

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
    // The connection lifecycle lives in the pure machine in liveConnection.ts;
    // this effect only owns the resources and runs the commands it returns.
    let connection: LiveConnection = INITIAL_LIVE_CONNECTION
    let socket: WebSocket | undefined
    let eventSource: EventSource | undefined
    let reconnectTimer: number | undefined
    let requestCount = 0

    const refresh = createLiveRefreshScheduler(() => { void refreshLiveQueries(queryClient) })

    // Inject the completed request into the recent-requests cache so the
    // activity feed shows it the moment the event lands; the scheduled refetch
    // then reconciles against the server's retained history.
    const injectLiveRequest = (request: InspectedRequest) => {
      queryClient.setQueryData<RequestsResponse>(queryKeys.requests, (old) => injectLiveRequestIntoCache(request, old))
    }

    const onEvent = (data: unknown) => {
      if (connection.kind === 'stopped' || typeof data !== 'string') return
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

    const dispatch = (event: LiveConnectionEvent) => {
      const step = transitionLiveConnection(connection, event)
      connection = step.state
      for (const command of step.commands) {
        switch (command.type) {
          case 'open-ws': openWebSocket(); break
          case 'open-sse': openEventSource(); break
          case 'schedule-reconnect':
            reconnectTimer = window.setTimeout(() => dispatch({ type: 'retry' }), command.delayMs)
            break
          case 'set-connected': setConnected(command.connected); break
          case 'refresh': refresh.schedule(); break
        }
      }
    }

    // EventSource reconnects on its own; the machine only surfaces its state.
    const openEventSource = () => {
      eventSource = new EventSource('/api/updates/sse')
      eventSource.addEventListener('open', () => dispatch({ type: 'sse-open' }))
      eventSource.addEventListener('message', (event) => onEvent(event.data))
      eventSource.addEventListener('error', () => dispatch({ type: 'sse-error' }))
    }

    const openWebSocket = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/updates/ws`)
      socket.addEventListener('open', () => dispatch({ type: 'ws-open' }))
      socket.addEventListener('message', (event) => onEvent(event.data))
      socket.addEventListener('close', () => dispatch({ type: 'ws-close' }))
    }

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        refresh.flush()
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange)

    dispatch({ type: 'start' })

    return () => {
      dispatch({ type: 'stop' })
      window.clearTimeout(reconnectTimer)
      refresh.cancel()
      document.removeEventListener('visibilitychange', onVisibilityChange)
      socket?.close()
      eventSource?.close()
    }
  }, [queryClient])

  return createElement(LiveStatsContext.Provider, { value: { connected, live } }, children)
}
