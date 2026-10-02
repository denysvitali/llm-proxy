import type { ModelStat } from '../../api'
import { fmtPct } from '../../format'

// fmtPct renders an exact zero as an em dash, which would hide a genuine
// 0% success rate (every request failed) or a 0% tool error rate. Render a
// real zero honestly; the truly-absent cases ('—') are guarded by callers.
export function pct(ratio: number, digits = 1): string {
  if (ratio === 0) return `${(0).toFixed(digits)}%`
  return fmtPct(ratio, digits)
}

export type StatsState = 'ready' | 'loading' | 'unavailable'

// Aggregate one backend's ModelStat rows into the numbers its card, the
// drawer, and the page summary all share.
export function backendAgg(models: ModelStat[], backendName: string) {
  const ms = models.filter((m) => m.backend === backendName)
  const requests = ms.reduce((s, m) => s + m.requests, 0)
  const successes = ms.reduce((s, m) => s + m.successes, 0)
  const toolCalls = ms.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = ms.reduce((s, m) => s + m.tool_errors, 0)
  const statusCodes: Record<string, number> = {}
  for (const m of ms) {
    for (const [code, n] of Object.entries(m.status_codes ?? {})) {
      statusCodes[code] = (statusCodes[code] ?? 0) + n
    }
  }
  return {
    requests,
    successes,
    uptime: requests ? successes / requests : 0,
    toolCalls,
    toolErrors,
    statusCodes,
  }
}
