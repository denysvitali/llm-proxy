import { Box, Card, Group, Text } from '@mantine/core'
import { useReducedMotion } from 'framer-motion'
import { useEffect, useId, useState, type ReactNode } from 'react'
import Sparkline from './Sparkline'
import { countUpFrame, countUpPhase } from '../lib/countUp'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
  /** Optional sparkline of the metric behind `value` (>=2 finite samples). */
  trend?: number[]
  /** Formats the scrubbed sparkline readout; defaults to a locale number. */
  trendFormat?: (value: number) => string
  /** Sets the value in the warning hue when it is a real number (e.g. below-99% success). */
  attention?: boolean
}

// One stat card: micro-label, measured value, hint, optional trend mark.
// `value` is usually a preformatted string; count-up applies only to finite
// numbers so formatted strings ("34.9K", "96.4%") are never mangled.
function useCountUp(value: ReactNode, enabled: boolean) {
  // Phases and frame math live in lib/countUp.ts.
  const phase = countUpPhase(value, enabled)
  const target = phase.kind === 'counting' ? phase.target : undefined
  const [shown, setShown] = useState<ReactNode>(phase.kind === 'counting' ? 0 : value)
  useEffect(() => {
    if (target === undefined) {
      setShown(value)
      return
    }
    let frame = 0
    const start = performance.now()
    const tick = (now: number) => {
      const step = countUpFrame(target, now - start)
      setShown(step.shown)
      if (!step.done) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [target, value])
  return shown
}

export default function StatTile({
  label,
  value,
  hint,
  icon,
  accent = 'gray',
  trend,
  trendFormat,
  attention = false,
}: StatTileProps) {
  const labelId = useId()
  const reduced = useReducedMotion() ?? false
  const displayed = useCountUp(value, !reduced)
  const hasValue = displayed !== null && displayed !== undefined && displayed !== '' && typeof displayed !== 'boolean'
    && !(typeof displayed === 'number' && !Number.isFinite(displayed))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''
  const hasTrend = Boolean(trend && trend.filter((point) => Number.isFinite(point)).length >= 2)

  return (
    <Card
      className="stat-tile"
      data-accent={accent}
      data-attention={attention || undefined}
      data-has-trend={hasTrend || undefined}
      role="group"
      aria-labelledby={labelId}
      p="md"
      miw={0}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mb={hasTrend ? 8 : 10}>
        <Text id={labelId} size="xs" c="dimmed" className="stat-tile-label" lh={1.35} style={{ minWidth: 0 }}>
          {label}
        </Text>
        {icon && (
          <span aria-hidden="true" className="stat-tile-icon">
            {icon}
          </span>
        )}
      </Group>
      <Box style={{ display: 'flex', alignItems: 'flex-start', minWidth: 0 }}>
        <div
          className="stat-tile-value"
          style={{ fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.03em', fontSize: 'clamp(22px, 2vw, 27px)', fontWeight: 700, lineHeight: 1.08, overflowWrap: 'anywhere' }}
        >
          {hasValue ? displayed : <span role="img" aria-label="No data">—</span>}
        </div>
      </Box>
      {hasHint && (
        <Box style={{ minWidth: 0 }} mt={4}>
          <Text size="xs" c="dimmed" lh={1.35} className="stat-tile-hint" style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
      {hasTrend && (
        <Box mt={10} className="stat-tile-trend">
          <Sparkline
            values={trend as number[]}
            height={32}
            format={trendFormat}
            ariaLabel={`${label} trend`}
          />
        </Box>
      )}
    </Card>
  )
}
