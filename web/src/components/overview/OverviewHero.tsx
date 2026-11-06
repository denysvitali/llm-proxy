import { Box, Card, Text } from '@mantine/core'
import { useId, useMemo, type ReactNode } from 'react'
import type { SeriesPoint } from '../../api'
import { useChartPalette } from '../../palette'

// The overview's first band: one elevated card split by hairlines into four
// measured tiles on a wide desktop, two-up below, stacked on phones. The
// primary requests tile also carries a sparkline of the selected range, so the
// band answers "how much, how healthy, how fast" in one glance.

type HeroAccent = 'warning' | 'critical' | undefined

export function HeroStat({
  label,
  value,
  hint,
  icon,
  accent,
  primary = false,
  children,
}: {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: HeroAccent
  primary?: boolean
  children?: ReactNode
}) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <div
      className="stat-tile overview-hero-tile"
      data-accent={accent}
      data-primary={primary || undefined}
      role="group"
      aria-labelledby={labelId}
    >
      <div className="overview-hero-head">
        <span id={labelId} className="overview-label">{label}</span>
        {icon && <span aria-hidden="true" className="stat-tile-icon overview-hero-icon">{icon}</span>}
      </div>
      <Text component="div" className="overview-hero-value">
        {hasValue ? value : <span role="img" aria-label="No data">—</span>}
      </Text>
      {hasHint && <Text component="div" className="overview-hero-hint">{hint}</Text>}
      {children}
    </div>
  )
}

// Requests sparkline for the active range. Unknown samples (NaN) break the
// line instead of pretending to be zero; the headline carries the number, so
// the mark itself is decorative for screen readers.
export function RequestSparkline({ points }: { points?: SeriesPoint[] }) {
  const palette = useChartPalette()
  const runs = useMemo(() => sparklineRuns(points), [points])
  if (runs.length === 0) {
    return <div className="overview-spark" aria-hidden="true" />
  }
  return (
    <div className="overview-spark" aria-hidden="true">
      <svg viewBox="0 0 100 30" preserveAspectRatio="none" role="presentation">
        <line x1="0" y1="29" x2="100" y2="29" stroke="var(--hairline)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        {runs.map((run, index) => run.length === 1 ? (
          <circle key={index} cx={run[0][0]} cy={run[0][1]} r="1.6" fill={palette.magnitude} vectorEffect="non-scaling-stroke" />
        ) : (
          <polyline
            key={index}
            points={run.map(([x, y]) => `${x},${y}`).join(' ')}
            fill="none"
            stroke={palette.magnitude}
            strokeWidth="1.75"
            strokeLinejoin="round"
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
          />
        ))}
      </svg>
    </div>
  )
}

type Point = [number, number]

function sparklineRuns(points?: SeriesPoint[]): Point[][] {
  const samples = points ?? []
  if (samples.length < 2) return []
  const finite = samples.map((point, index) => ({ index, value: point.value }))
    .filter((sample) => Number.isFinite(sample.value))
  if (finite.length < 2) return []

  const low = Math.min(...finite.map((sample) => sample.value))
  const high = Math.max(...finite.map((sample) => sample.value))
  const span = high - low || 1

  const runs: Point[][] = []
  let current: Point[] = []
  samples.forEach((point, index) => {
    if (!Number.isFinite(point.value)) {
      if (current.length) runs.push(current)
      current = []
      return
    }
    const x = (index / (samples.length - 1)) * 100
    const y = 29 - ((point.value - low) / span) * 26
    current.push([x, y])
  })
  if (current.length) runs.push(current)
  return runs
}

export function OverviewSummaryMeta({ modelsWithTraffic, toolCalls, toolErrors }: {
  modelsWithTraffic: number
  toolCalls: number
  toolErrors: number
}) {
  const toolErrorRate = toolCalls > 0 ? (100 * Math.min(toolErrors / toolCalls, 1)).toFixed(1) : undefined
  return (
    <Box className="overview-summary-footer" role="list" aria-label="Traffic summary details">
      <div className="overview-summary-item" role="listitem">
        <span className="overview-label">Models with traffic</span>
        <span className="overview-summary-value">{modelsWithTraffic}</span>
      </div>
      <div className="overview-summary-item" role="listitem">
        <span className="overview-label">Tool calls</span>
        <span className="overview-summary-value">{toolCalls.toLocaleString('en-US')}</span>
      </div>
      <div className="overview-summary-item" role="listitem" data-tone={toolErrors > 0 ? 'warning' : undefined}>
        <span className="overview-label">Tool errors</span>
        <span className="overview-summary-value">
          {toolErrors.toLocaleString('en-US')}
          {toolErrorRate !== undefined && <span className="overview-summary-suffix"> · {toolErrorRate}%</span>}
        </span>
      </div>
    </Box>
  )
}

export default function OverviewHeroBand({ children }: { children: ReactNode }) {
  return (
    <Card className="overview-stat-band" padding={0} miw={0}>
      <div className="overview-stat-grid">{children}</div>
    </Card>
  )
}
