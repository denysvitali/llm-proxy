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

// Dense control-room KPI tile.
//
// The number is the hero and everything else recedes: a small mono icon, a
// micro-caps label, then a large tabular figure that bottom-aligns so a row of
// six tiles shares one baseline. The hint sits under a hairline rather than in
// a card of its own, so a KPI band reads as one dense strip instead of six
// competing boxes (DESIGN.md §1, §4).
//
// The value uses the `stat-value` class (mono + tabular-nums from index.css):
// operators scan these vertically, and proportional figures make a column
// shimmer. A missing value renders an em dash with an accessible label rather
// than a blank, because "no data" and "zero" are different facts.
export default function StatTile({ label, value, hint, icon, accent = 'brand' }: StatTileProps) {
  const labelId = useId()
  const hasValue = value !== null && value !== undefined && value !== '' && typeof value !== 'boolean'
    && !(typeof value === 'number' && !Number.isFinite(value))
  const hasHint = hint !== null && hint !== undefined && hint !== false && hint !== ''

  // The icon is a quiet monochrome cue, not a colored badge: in a row of six,
  // six saturated chips compete with the figures they annotate. The accent is
  // kept as a prop for callers and applied only to the icon's stroke color.
  const accentColor: Record<Accent, string> = {
    brand: 'var(--mantine-primary-color-filled)',
    teal: 'var(--data-good)',
    orange: 'var(--data-serious)',
    grape: 'var(--data-critical)',
    gray: 'var(--mantine-color-dimmed)',
    red: 'var(--data-critical)',
  }

  return (
    <Paper
      withBorder
      p="md"
      radius="lg"
      role="group"
      aria-labelledby={labelId}
      h="100%"
      miw={0}
      style={{ display: 'flex', flexDirection: 'column' }}
    >
      <Group justify="space-between" align="center" wrap="nowrap" mb={6} gap="xs">
        <Text
          id={labelId}
          fz={10.5}
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
              color: accentColor[accent],
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
          fz={26}
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
