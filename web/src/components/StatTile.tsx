import { Box, Card, Group, Text } from '@mantine/core'
import { useId, type ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// One stat card in the overview band: icon chip, label, measured value,
// hint. The optional accent re-tints the icon chip (e.g. the success-rate
// tile turns warning-colored under 99%).
export default function StatTile({ label, value, hint, icon, accent = 'gray' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <Card
      className="stat-tile"
      data-accent={accent}
      role="group"
      aria-labelledby={labelId}
      p="md"
      miw={0}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mb={10}>
        <Text id={labelId} size="xs" fw={600} c="dimmed" lh={1.35} style={{ minWidth: 0 }}>
          {label}
        </Text>
        {icon && (
          <span aria-hidden="true" className="stat-tile-icon">
            {icon}
          </span>
        )}
      </Group>
      <Box style={{ display: 'flex', alignItems: 'flex-start', minWidth: 0 }}>
        <Text
          component="div"
          fz={24}
          fw={700}
          lh={1.1}
          style={{ overflowWrap: 'anywhere', fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.02em' }}
        >
          {hasValue ? value : <span role="img" aria-label="No data">—</span>}
        </Text>
      </Box>
      {hasHint && (
        <Box style={{ minWidth: 0 }} mt={4}>
          <Text size="xs" c="dimmed" lh={1.35} style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
    </Card>
  )
}
