import type { ModelStat } from '../api'
import type { MixSegment } from '../components/TokenMixBar'

// Fixed token kind order — never changes
export const TOKEN_KINDS = ['input', 'output', 'cache read', 'cache write'] as const

export function providerSegments(models: ModelStat[], colors: string[]): [string, MixSegment[]][] {
  const kinds = [
    (m: ModelStat) => m.input_tokens,
    (m: ModelStat) => m.output_tokens,
    (m: ModelStat) => m.cache_read_tokens,
    (m: ModelStat) => m.cache_write_tokens,
  ]
  const names = ['input', 'output', 'cache read', 'cache write']
  const byBackend = new Map<string, MixSegment[]>()
  for (const model of models) {
    let segments = byBackend.get(model.backend)
    if (!segments) {
      segments = names.map((name, i) => ({ name, color: colors[i], value: 0 }))
      byBackend.set(model.backend, segments)
    }
    kinds.forEach((get, i) => { segments[i].value += get(model) })
  }
  return [...byBackend.entries()]
}

export function providerAggregates(models: ModelStat[]) {
  const byBackend = new Map<string, { requests: number; successes: number; toolCalls: number; toolErrors: number; statusCodes: Record<string, number> }>()
  for (const m of models) {
    const cur = byBackend.get(m.backend) ?? { requests: 0, successes: 0, toolCalls: 0, toolErrors: 0, statusCodes: {} }
    cur.requests += m.requests
    cur.successes += m.successes
    cur.toolCalls += m.tool_calls
    cur.toolErrors += m.tool_errors
    for (const [code, n] of Object.entries(m.status_codes ?? {})) {
      cur.statusCodes[code] = (cur.statusCodes[code] ?? 0) + n
    }
    byBackend.set(m.backend, cur)
  }
  return [...byBackend.entries()].map(([backend, v]) => ({
    backend,
    requests: v.requests,
    uptime: v.requests ? v.successes / v.requests : 0,
    toolCalls: v.toolCalls,
    toolErrors: v.toolErrors,
    statusCodes: v.statusCodes,
  }))
}

// Provider health from recorded traffic: no requests → no-traffic; otherwise
// >=99% success healthy, >=90% degraded, below that unhealthy.
export type HealthState = 'healthy' | 'degraded' | 'unhealthy' | 'no-traffic'

export function healthState(requests: number, uptime: number): HealthState {
  if (requests <= 0) return 'no-traffic'
  return uptime >= 0.99 ? 'healthy' : uptime >= 0.9 ? 'degraded' : 'unhealthy'
}

export type UptimeTone = 'good' | 'warning' | 'critical'

export function uptimeState(uptime: number): UptimeTone {
  return uptime >= 0.99 ? 'good' : uptime >= 0.9 ? 'warning' : 'critical'
}

// What an availability badge can show, first match wins:
//   no-traffic   requests is exactly 0
//   unavailable  requests or uptime is not a usable number (callers pass NaN
//                while stats are loading or failed), or uptime is outside 0..1
//   measured     a real success ratio over a real request count
export type UptimeReading =
  | { kind: 'no-traffic' }
  | { kind: 'unavailable' }
  | { kind: 'measured'; tone: UptimeTone; uptime: number; requests: number }

export function uptimeReading(uptime: number, requests: number): UptimeReading {
  if (requests === 0) return { kind: 'no-traffic' }
  if (!Number.isFinite(requests) || requests < 0 || !Number.isFinite(uptime) || uptime < 0 || uptime > 1) return { kind: 'unavailable' }
  return { kind: 'measured', tone: uptimeState(uptime), uptime, requests }
}

export function mixSegments(m: ModelStat, colors: string[]): MixSegment[] {
  return [
    { name: 'input', color: colors[0], value: m.input_tokens },
    { name: 'output', color: colors[1], value: m.output_tokens },
    { name: 'cache read', color: colors[2], value: m.cache_read_tokens },
    { name: 'cache write', color: colors[3], value: m.cache_write_tokens },
  ]
}
