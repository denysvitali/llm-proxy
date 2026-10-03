// Number formatting for stat tiles, tables, and axis labels.
// Every helper renders a real zero as a real zero: an em dash is
// reserved for the unknown/absent case (AGENTS.md — never conflate
// unknown statistics with zero). Callers guard the absent cases.
export function fmtInt(n: number): string {
  if (!Number.isFinite(n)) return '—'
  if (n >= 1e9) return `${trimZeros(n / 1e9)}B`
  if (n >= 1e6) return `${trimZeros(n / 1e6)}M`
  if (n >= 1e4) return `${trimZeros(n / 1e3)}K`
  return n.toLocaleString('en-US')
}

// Compact notation keeps one significant decimal but drops a trailing
// ".0" so axis labels and tile hints read 1.5K rather than 1.5K / 2.0K.
function trimZeros(v: number): string {
  return v.toFixed(1).replace(/\.0$/, '')
}

export function fmtSec(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '—'
  if (s === 0) return '0s'
  if (s < 0.001) return `${Math.round(s * 1e6)}µs`
  if (s < 1) return `${Math.round(s * 1000)}ms`
  return `${s.toFixed(s < 10 ? 2 : 1)}s`
}

export function fmtPct(ratio: number, digits = 1): string {
  if (!Number.isFinite(ratio) || ratio < 0) return '—'
  if (ratio === 0) return `${(0).toFixed(digits)}%`
  return `${(ratio * 100).toFixed(digits)}%`
}

// Tool errors and their calls are recorded a turn apart, so the raw ratio can
// briefly exceed 1 across a bucket boundary; a percentage over 100% is never
// a true state, so rates derived from mismatched counters clamp to [0, 1].
export function clampRate(ratio: number): number {
  return Number.isFinite(ratio) ? Math.min(Math.max(ratio, 0), 1) : 0
}

export function fmtTps(v: number): string {
  if (!Number.isFinite(v) || v < 0) return '—'
  if (v === 0) return '0'
  return v < 10 ? v.toFixed(1) : v.toFixed(0)
}

export function fmtTime(d: Date): string {
  if (!Number.isFinite(d.getTime())) return '—'
  return d.toLocaleTimeString('en-US', { hour12: false })
}
