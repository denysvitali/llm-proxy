import { Box, Group, Paper, Text, ThemeIcon } from '@mantine/core'
import type { ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// KPI stat tile: accent icon chip, muted uppercase label, headline number in
// tabular figures. A thin accent bar on the left ties the tile to its metric
// family without relying on color alone (the icon + label carry meaning).
export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  return (
    <Paper withBorder p="md" radius="lg" style={{ position: 'relative', overflow: 'hidden' }}>
      <Box
        style={{
          position: 'absolute',
          insetBlock: 0,
          insetInlineStart: 0,
          width: 3,
          background: `var(--mantine-color-${accent}-filled)`,
          opacity: 0.9,
        }}
      />
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb={6}>
        <Text size="xs" tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '0.03em' }}>
          {label}
        </Text>
        {icon && (
          <ThemeIcon variant="light" color={accent} size="md" radius="md">
            {icon}
          </ThemeIcon>
        )}
      </Group>
      <Text fz={28} fw={700} lh={1.1} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {value}
      </Text>
      {hint && (
        <Text size="xs" c="dimmed" mt={6}>
          {hint}
        </Text>
      )}
    </Paper>
  )
}
