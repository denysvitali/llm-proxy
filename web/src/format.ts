// Number formatting for stat tiles, tables, and axis labels.
export function fmtInt(n: number): string {
  if (!Number.isFinite(n)) return '—'
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e4) return `${(n / 1e3).toFixed(1)}K`
  return n.toLocaleString('en-US')
}

export function fmtSec(s: number): string {
  if (!Number.isFinite(s) || s <= 0) return '—'
  if (s < 0.001) return `${(s * 1e6).toFixed(0)}µs`
  if (s < 1) return `${Math.round(s * 1000)}ms`
  return `${s.toFixed(s < 10 ? 2 : 1)}s`
}

export function fmtPct(ratio: number, digits = 1): string {
  if (!Number.isFinite(ratio) || ratio <= 0) return '—'
  return `${(ratio * 100).toFixed(digits)}%`
}

export function fmtTps(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return '—'
  return v < 10 ? v.toFixed(1) : v.toFixed(0)
}

export function fmtTime(d: Date): string {
  return d.toLocaleTimeString('en-US', { hour12: false })
}
