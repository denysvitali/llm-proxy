import { Group, Paper, Text, ThemeIcon } from '@mantine/core'
import { useId, type ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// Quiet label, prominent value, and a small tinted icon chip. Keep full labels
// readable on narrow cards rather than hiding the metric's identity.
export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))

  return (
    <Paper withBorder p="lg" radius="lg" role="group" aria-labelledby={labelId} h="100%" miw={0}>
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb={12} gap="xs">
        <Text
          id={labelId}
          size="xs"
          tt="uppercase"
          c="dimmed"
          fw={600}
          style={{ letterSpacing: '0.05em', overflowWrap: 'anywhere', minWidth: 0 }}
        >
          {label}
        </Text>
        {icon && (
          <ThemeIcon
            aria-hidden="true"
            variant="light"
            color={accent}
            size="sm"
            radius="md"
            style={{ flexShrink: 0 }}
          >
            {icon}
          </ThemeIcon>
        )}
      </Group>
      <Text
        fz={32}
        fw={700}
        lh={1.1}
        style={{ letterSpacing: '-0.03em', overflowWrap: 'anywhere' }}
      >
        {hasValue ? value : <span aria-label="No data">—</span>}
      </Text>
      {hint !== null && hint !== undefined && hint !== false && hint !== '' && (
        <Text size="xs" c="dimmed" mt={6} lh={1.35} style={{ overflowWrap: 'anywhere' }}>
          {hint}
        </Text>
      )}
    </Paper>
  )
}
