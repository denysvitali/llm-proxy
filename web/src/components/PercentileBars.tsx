import { Table, Text, Tooltip } from '@mantine/core'
import { motion, useReducedMotion } from 'framer-motion'
import { useChartPalette } from '../palette'
import { fmtSec, fmtTps } from '../format'

interface PercentileBarsProps {
  values: { p50: number; p90: number; p99: number }
  unit: 's' | 'tok/s'
  /** Shared upper bound so sibling charts (TTFT vs E2E) read on one scale. */
  max?: number
}

const labels = ['p50', 'p90', 'p99'] as const

// Three ordered percentiles ride one hue ramp (light p50 → dark p99): the
// category order is meaningful, so a categorical palette would be wrong here.
// Bar length is the measured value on the caller's shared scale; an
// unobserved percentile keeps its em dash and draws no mark.
function isObserved(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

export default function PercentileBars({ values, unit, max }: PercentileBarsProps) {
  const reduced = useReducedMotion() ?? false
  const palette = useChartPalette()
  const observed = labels.map((label) => values[label]).filter(isObserved)
  // Keep the caller's shared scale; missing observations must not poison it.
  const scaleMax = max !== undefined && isObserved(max) ? max : Math.max(0, ...observed)
  const fmt = unit === 's' ? fmtSec : fmtTps

  return (
    <Table
      layout="fixed"
      verticalSpacing={2}
      horizontalSpacing={0}
      withRowBorders={false}
      captionSide="bottom"
      className="percentile-bars"
    >
      <Table.Caption ta="left" mt={6}>
        {scaleMax > 0
          ? `0–${fmt(scaleMax)}${unit === 'tok/s' ? ' tok/s' : ''}${max !== undefined && isObserved(max) ? ' · shared scale' : ''}`
          : 'No observations yet'}
      </Table.Caption>
      <Table.Thead>
        <Table.Tr>
          <Table.Th w={96}><Text className="micro-label" component="span">Percentile</Text></Table.Th>
          <Table.Th><Text className="micro-label" component="span">Distribution</Text></Table.Th>
          <Table.Th w={72} ta="right"><Text className="micro-label" component="span">{unit === 's' ? 'Time' : 'tok/s'}</Text></Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {labels.map((label, index) => {
          const value = values[label]
          const seen = isObserved(value)
          const detail = seen ? `${fmt(value)}${unit === 'tok/s' ? ' tok/s' : ''}` : 'No observations'
          const ratio = seen && scaleMax > 0 ? Math.min(1, value / scaleMax) : 0
          const color = palette.ramp[Math.min(index, palette.ramp.length - 1)]
          return (
            <Tooltip key={label} label={`${label}: ${detail}`} events={{ hover: true, focus: true, touch: true }} withArrow>
              <Table.Tr tabIndex={0} className="mantine-focus-auto percentile-row">
                <Table.Th scope="row"><Text size="xs" className="tabular" c="dimmed" fw={600}>{label}</Text></Table.Th>
                <Table.Td>
                  <span
                    className="percentile-track"
                    aria-hidden="true"
                    style={{ '--pc-color': color } as React.CSSProperties}
                  >
                    {seen && (
                      <motion.span
                        className="percentile-fill"
                        initial={reduced ? { width: `${ratio * 100}%` } : { width: 0 }}
                        animate={{ width: `${Math.max(ratio * 100, 1.5)}%` }}
                        transition={reduced ? { duration: 0 } : { duration: 0.5, ease: [0.23, 1, 0.32, 1], delay: index * 0.05 }}
                      />
                    )}
                  </span>
                </Table.Td>
                <Table.Td ta="right" pl="xs" aria-label={seen ? detail : 'No observations'}>
                  <Text size="sm" fw={600} className="tabular" c={seen ? undefined : 'dimmed'} style={{ overflowWrap: 'anywhere' }}>
                    {seen ? fmt(value) : '—'}
                  </Text>
                </Table.Td>
              </Table.Tr>
            </Tooltip>
          )
        })}
      </Table.Tbody>
    </Table>
  )
}
