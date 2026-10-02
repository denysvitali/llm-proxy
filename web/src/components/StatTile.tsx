import { Box, Group, Paper, Text } from '@mantine/core'
import { useId, type ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <Paper
      className="stat-tile"
      data-accent={accent}
      withBorder
      radius="lg"
      role="group"
      aria-labelledby={labelId}
      h="100%"
      miw={0}
      style={{ display: 'flex', flexDirection: 'column' }}
    >
      <Group justify="space-between" align="center" wrap="nowrap" mb={14} gap="xs">
        <Text
          id={labelId}
          fz={12}
          c="dimmed"
          fw={600}
          lh={1.3}
          style={{ overflowWrap: 'anywhere', minWidth: 0 }}
        >
          {label}
        </Text>
        {icon && (
          <span
            aria-hidden="true"
            className="stat-icon"
          >
            {icon}
          </span>
        )}
      </Group>
      <Box style={{ display: 'flex', alignItems: 'flex-start', minWidth: 0 }}>
        <Text
          component="div"
          className="stat-value"
          fw={700}
          lh={1.15}
          style={{ overflowWrap: 'anywhere' }}
        >
          {hasValue ? value : <span role="img" aria-label="No data">—</span>}
        </Text>
      </Box>
      {hasHint && (
        <Box mt={10} style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" lh={1.35} style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
    </Paper>
  )
}
