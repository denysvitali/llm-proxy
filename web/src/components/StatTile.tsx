import { Box, Group, Text } from '@mantine/core'
import { useId, type ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// One tile inside a divided stat band (see .stat-band in index.css): the band
// surface carries the card chrome, tiles are separated by hairline gaps.
export default function StatTile({ label, value, hint, icon, accent = 'gray' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <Box
      className="stat-tile"
      data-accent={accent}
      role="group"
      aria-labelledby={labelId}
    >
      <Group className="stat-head" justify="space-between" align="center" wrap="nowrap" gap="xs">
        <Text id={labelId} className="stat-label" lh={1.35} style={{ minWidth: 0 }}>
          {label}
        </Text>
        {icon && (
          <span aria-hidden="true" className="stat-icon">
            {icon}
          </span>
        )}
      </Group>
      <Box style={{ display: 'flex', alignItems: 'flex-start', minWidth: 0 }}>
        <Text
          component="div"
          className="stat-value"
          lh={1.1}
          style={{ overflowWrap: 'anywhere' }}
        >
          {hasValue ? value : <span role="img" aria-label="No data">—</span>}
        </Text>
      </Box>
      {hasHint && (
        <Box className="stat-hint" style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" lh={1.35} style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
    </Box>
  )
}
