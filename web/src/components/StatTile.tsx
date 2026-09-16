import { Box, Group, Paper, Text, ThemeIcon } from '@mantine/core'
import { useId, type ReactNode } from 'react'

type Accent = 'brand' | 'teal' | 'orange' | 'grape' | 'gray' | 'red'

interface StatTileProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: Accent
}

// Apple-style KPI tile: uppercase micro-label, big tabular-nums figure, and a
// dimmed meta line under a hairline separator. The tile is a flex column so
// the figure bottom-aligns and the separator+meta sit at the same height
// across a grid row. Keep full labels readable on narrow cards rather than
// hiding the metric's identity.
export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  return (
    <Paper
      withBorder
      p="lg"
      radius="lg"
      role="group"
      aria-labelledby={labelId}
      h="100%"
      miw={0}
      style={{ display: 'flex', flexDirection: 'column' }}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb="sm" gap="xs">
        <Text
          id={labelId}
          fz={11}
          tt="uppercase"
          c="dimmed"
          fw={600}
          lh={1.3}
          style={{ letterSpacing: '0.06em', overflowWrap: 'anywhere', minWidth: 0 }}
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
      <Box style={{ flex: 1, display: 'flex', alignItems: 'flex-end', minWidth: 0 }}>
        <Text
          component="div"
          fz={32}
          fw={700}
          lh={1.1}
          style={{
            letterSpacing: '-0.02em',
            fontVariantNumeric: 'tabular-nums',
            overflowWrap: 'anywhere',
          }}
        >
          {hasValue ? value : <span role="img" aria-label="No data">—</span>}
        </Text>
      </Box>
      {hasHint && (
        <Box mt="sm" pt={10} style={{ borderTop: '1px solid var(--mantine-color-default-border)', minWidth: 0 }}>
          <Text size="xs" c="dimmed" lh={1.35} style={{ overflowWrap: 'anywhere' }}>
            {hint}
          </Text>
        </Box>
      )}
    </Paper>
  )
}
