import { Box, Text } from '@mantine/core'

export interface MixSegment {
  name: string
  color: string
  value: number
}

// Stacked composition bar for token kinds within one row. Fixed categorical
// slot colors per kind; 2px surface gaps between segments; per-segment
// tooltip titles and a legend-with-values beside/below so no number is
// encoded by color alone.
export default function TokenMixBar({
  segments,
  height = 16,
}: {
  segments: MixSegment[]
  height?: number
}) {
  const total = segments.reduce((s, x) => s + x.value, 0)
  if (total <= 0) {
    return (
      <Text size="sm" c="dimmed">
        no tokens yet
      </Text>
    )
  }
  const visible = segments.filter((s) => s.value > 0)
  return (
    <Box
      style={{
        display: 'flex',
        gap: 2,
        height,
        borderRadius: 4,
        overflow: 'hidden',
      }}
    >
      {visible.map((s) => (
        <div
          key={s.name}
          title={`${s.name}: ${s.value.toLocaleString('en-US')} tok (${((s.value / total) * 100).toFixed(1)}%)`}
          style={{
            flexGrow: s.value,
            flexBasis: 0,
            background: s.color,
            minWidth: 2,
          }}
        />
      ))}
    </Box>
  )
}

export function TokenLegend({ segments }: { segments: MixSegment[] }) {
  return (
    <Box style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', marginTop: 6 }}>
      {segments
        .filter((s) => s.value > 0)
        .map((s) => (
          <Box key={s.name} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span
              style={{
                width: 9,
                height: 9,
                borderRadius: 2,
                background: s.color,
                display: 'inline-block',
              }}
            />
            {/* Text wears text tokens; the swatch carries identity only. */}
            <Text size="xs" c="dimmed">
              {s.name} {s.value.toLocaleString('en-US')}
            </Text>
          </Box>
        ))}
    </Box>
  )
}
