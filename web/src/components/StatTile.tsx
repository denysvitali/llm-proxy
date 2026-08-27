import { Group, Paper, Text, ThemeIcon } from '@mantine/core'
import type { ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// KPI stat tile, Apple style: quiet uppercase label, SF-weighted headline in
// tabular figures, small tinted icon chip. No accent bar — hierarchy comes
// from type, not decoration. The icon chip's tint is the only color.
export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  return (
    <Paper withBorder p="md" radius="lg">
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb={10} gap="xs">
        <Text
          size="xs"
          tt="uppercase"
          c="dimmed"
          fw={600}
          style={{ letterSpacing: '0.05em' }}
          lineClamp={1}
        >
          {label}
        </Text>
        {icon && (
          <ThemeIcon
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
        fz={26}
        fw={700}
        lh={1.1}
        style={{
          fontVariantNumeric: 'tabular-nums',
          letterSpacing: '-0.02em',
        }}
      >
        {value}
      </Text>
      {hint && (
        <Text size="xs" c="dimmed" mt={6} lh={1.35}>
          {hint}
        </Text>
      )}
    </Paper>
  )
}
