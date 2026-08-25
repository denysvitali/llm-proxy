import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

const INITIAL_RECONNECT_DELAY_MS = 1_000
const MAX_RECONNECT_DELAY_MS = 15_000

export function useLiveStatsUpdates() {
  const queryClient = useQueryClient()
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    let socket: WebSocket | undefined
    let reconnectTimer: number | undefined
    let retryDelay = INITIAL_RECONNECT_DELAY_MS
    let stopped = false

    const connect = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/updates/ws`)

      socket.addEventListener('open', () => {
        retryDelay = INITIAL_RECONNECT_DELAY_MS
        setConnected(true)
      })

      socket.addEventListener('message', (event) => {
        if ((event.data as string).startsWith('{"type":"stats-updated"}')) {
          void queryClient.invalidateQueries({ queryKey: ['stats'] })
          void queryClient.invalidateQueries({ queryKey: ['stats-series'] })
          void queryClient.invalidateQueries({ queryKey: ['overview'] })
        }
      })

      socket.addEventListener('close', () => {
        setConnected(false)
        if (!stopped) {
          reconnectTimer = window.setTimeout(connect, retryDelay)
          retryDelay = Math.min(retryDelay * 2, MAX_RECONNECT_DELAY_MS)
        }
      })
    }

    connect()

    return () => {
      stopped = true
      window.clearTimeout(reconnectTimer)
      socket?.close()
    }
  }, [queryClient])

  return connected
}
