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
  void accent // accepted for backward compatibility; all accents resolve to neutral
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <Paper
      className="stat-tile"
      withBorder
      p="md"
      radius="md"
      role="group"
      aria-labelledby={labelId}
      h="100%"
      miw={0}
      style={{ display: 'flex', flexDirection: 'column' }}
    >
      <Group justify="space-between" align="center" wrap="nowrap" mb={6} gap="xs">
        <Text
          id={labelId}
          fz={13}
          tt="uppercase"
          c="dimmed"
          fw={600}
          lh={1.3}
          style={{ letterSpacing: '0.07em', overflowWrap: 'anywhere', minWidth: 0 }}
        >
          {label}
        </Text>
        {icon && (
          <span
            aria-hidden="true"
            style={{
              display: 'inline-flex',
              flexShrink: 0,
              color: 'var(--mantine-color-text)',
              opacity: 0.85,
            }}
          >
            {icon}
          </span>
        )}
      </Group>
      <Box style={{ flex: 1, display: 'flex', alignItems: 'flex-end', minWidth: 0 }}>
        <Text
          component="div"
          className="stat-value"
          fz={28}
          fw={600}
          lh={1.15}
          style={{ overflowWrap: 'anywhere' }}
        >
          {hasValue ? value : <span role="img" aria-label="No data">—</span>}
        </Text>
      </Box>
      {hasHint && (
        <Box mt={8} pt={8} style={{ borderTop: '1px solid var(--hairline)', minWidth: 0 }}>
          <Text size="xs" c="dimmed" lh={1.35} style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
    </Paper>
  )
}
