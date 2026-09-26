import { Progress, Table, Text, Tooltip } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'
import { fmtSec, fmtTps } from '../format'

interface PercentileBarsProps {
  values: { p50: number; p90: number; p99: number }
  unit: 's' | 'tok/s'
  /** Shared upper bound so sibling charts (TTFT vs E2E) read on one scale. */
  max?: number
}

const labels = ['p50', 'p90', 'p99'] as const

export default function PercentileBars({ values, unit, max }: PercentileBarsProps) {
  const reduceMotion = useReducedMotion()
  const observedValues = labels.map((label) => values[label]).filter(isObserved)
  // Keep the caller's shared scale; missing observations must not poison it.
  const scaleMax = max !== undefined && isObserved(max)
    ? max
    : Math.max(0, ...observedValues)
  const fmt = unit === 's' ? fmtSec : fmtTps

  return (
    <Table
      layout="fixed"
      verticalSpacing={6}
      horizontalSpacing={0}
      withRowBorders={false}
      highlightOnHover
      captionSide="bottom"
      style={{ fontVariantNumeric: 'tabular-nums' }}
    >
      <Table.Caption ta="left" mt={4}>
        {scaleMax > 0
          ? `Scale: 0–${fmt(scaleMax)}${unit === 'tok/s' ? ' tok/s' : ''}${max !== undefined && isObserved(max) ? ' · shared' : ''}`
          : 'No observations yet'}
      </Table.Caption>
      <Table.Thead>
        <Table.Tr>
          <Table.Th w={72}>
            <Text size="xs" c="dimmed" fw={500}>Percentile</Text>
          </Table.Th>
          <Table.Th>
            <Text size="xs" c="dimmed" fw={500}>Distribution</Text>
          </Table.Th>
          <Table.Th w={80} ta="right">
            <Text size="xs" c="dimmed" fw={500}>{unit === 's' ? 'Time' : 'tok/s'}</Text>
          </Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {labels.map((label) => {
          const val = values[label]
          const observed = isObserved(val)
          const detail = observed
            ? `${fmt(val)}${unit === 'tok/s' ? ' tok/s' : ''}`
            : 'No observations'
          const width = observed && scaleMax > 0
            ? Math.min(100, Math.max((val / scaleMax) * 100, 1.5))
            : 0

          return (
            <Tooltip
              key={label}
              label={`${label}: ${detail}`}
              events={{ hover: true, focus: true, touch: true }}
              withArrow
            >
              <Table.Tr tabIndex={0} className="mantine-focus-auto" h={44}>
                <Table.Th scope="row">
                  <Text size="sm" fw={500}>{label}</Text>
                </Table.Th>
                <Table.Td>
                  <Progress
                    aria-hidden="true"
                    value={width}
                    color="var(--mantine-color-text)"
                    size={10}
                    radius={0}
                    transitionDuration={reduceMotion ? 0 : 250}
                    styles={{ section: { borderRadius: '0 4px 4px 0' } }}
                  />
                </Table.Td>
                <Table.Td ta="right" pl="xs" aria-label={observed ? detail : 'No observations'}>
                  <Text size="sm" fw={500} c={observed ? undefined : 'dimmed'} style={{ overflowWrap: 'anywhere' }}>
                    {observed ? fmt(val) : '—'}
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

function isObserved(value: number): boolean {
  return Number.isFinite(value) && value > 0
}
