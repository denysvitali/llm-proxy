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
  const byBackend = new Map<string, ModelStat[]>()
  for (const m of models) {
    byBackend.set(m.backend, [...(byBackend.get(m.backend) ?? []), m])
  }
  return [...byBackend.entries()].map(([backend, ms]) => [
    backend,
    kinds.map((get, i) => ({
      name: names[i],
      color: colors[i],
      value: ms.reduce((s, m) => s + get(m), 0),
    })),
  ])
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

export type HealthState = 'healthy' | 'degraded' | 'unhealthy' | 'no-traffic'

export function healthState(requests: number, uptime: number): HealthState {
  if (requests <= 0) return 'no-traffic'
  return uptime >= 0.99 ? 'healthy' : uptime >= 0.9 ? 'degraded' : 'unhealthy'
}

export function uptimeState(uptime: number): 'good' | 'warning' | 'critical' {
  return uptime >= 0.99 ? 'good' : uptime >= 0.9 ? 'warning' : 'critical'
}

export function mixSegments(m: ModelStat, colors: string[]): MixSegment[] {
  return [
    { name: 'input', color: colors[0], value: m.input_tokens },
    { name: 'output', color: colors[1], value: m.output_tokens },
    { name: 'cache read', color: colors[2], value: m.cache_read_tokens },
    { name: 'cache write', color: colors[3], value: m.cache_write_tokens },
  ]
}
