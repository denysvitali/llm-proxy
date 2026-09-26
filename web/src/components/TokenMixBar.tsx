import { Box, Text, Tooltip, VisuallyHidden } from '@mantine/core'

export interface MixSegment {
  name: string
  color: string
  value: number
}

function describeSegment(segment: MixSegment, total: number): string {
  return `${segment.name}: ${segment.value.toLocaleString('en-US')} tokens (${((segment.value / total) * 100).toFixed(1)}%)`
}

// Invalid counts cannot describe a meaningful composition. Keep missing data
// distinct from a valid, all-zero mix instead of drawing a misleading bar.
function mixTotal(segments: MixSegment[]): number {
  if (segments.some((s) => !Number.isFinite(s.value) || s.value < 0)) return NaN
  return segments.reduce((sum, segment) => sum + segment.value, 0)
}

// Fixed categorical colors and 2px surface gaps. The full mix is available on
// keyboard focus; each mark also has a hover tooltip. Pair with TokenLegend.
export default function TokenMixBar({
  segments,
  height = 16,
}: {
  segments: MixSegment[]
  height?: number
}) {
  const total = mixTotal(segments)
  if (!Number.isFinite(total) || total <= 0) {
    return (
      <Text size="sm" c="dimmed">
        {Number.isFinite(total) ? 'no tokens yet' : 'token data unavailable'}
      </Text>
    )
  }
  const visible = segments.filter((s) => s.value > 0)
  const description = `Token mix: ${visible.map((s) => describeSegment(s, total)).join('; ')}`
  return (
    <Box
      role="figure"
      aria-labelledby={`token-mix-desc-${visible.map((s) => s.name).join('-')}`}
      style={{
        display: 'flex',
        gap: 2,
        height: Number.isFinite(height) && height > 0 ? height : 16,
        borderRadius: 4,
        minWidth: 0,
      }}
    >
      <span id={`token-mix-desc-${visible.map((s) => s.name).join('-')}`} style={{ display: 'none' }}>
        {description}
      </span>
      {visible.map((s, index) => (
        <Tooltip key={s.name} label={describeSegment(s, total)} withArrow>
          <div
            style={{
              flexGrow: s.value / total,
              flexBasis: 0,
              background: s.color,
              minWidth: 0,
              borderTopLeftRadius: index === 0 ? 4 : 0,
              borderBottomLeftRadius: index === 0 ? 4 : 0,
              borderTopRightRadius: index === visible.length - 1 ? 4 : 0,
              borderBottomRightRadius: index === visible.length - 1 ? 4 : 0,
            }}
          />
        </Tooltip>
      ))}
    </Box>
  )
}

// Counts by default; compact legends show shares while retaining exact counts
// for assistive technology. Names and values remain readable without color.
export function TokenLegend({
  segments,
  showPercent = false,
  compact = false,
}: {
  segments: MixSegment[]
  showPercent?: boolean
  compact?: boolean
}) {
  const total = mixTotal(segments)
  if (!Number.isFinite(total) || total <= 0) return null

  return (
    <Box role="list" aria-label="Token breakdown" style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', marginTop: 6 }}>
      {segments.filter((s) => s.value > 0).map((s) => (
        <Box key={s.name} role="listitem" style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
          {/* Data-mark swatch: a colored dot is honest as a styled Box, not chrome */}
          <Box
            aria-hidden="true"
            style={{
              width: 9,
              height: 9,
              flexShrink: 0,
              borderRadius: 2,
              background: s.color,
            }}
          />
          <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere', fontVariantNumeric: 'tabular-nums' }}>
            {s.name} {!compact && s.value.toLocaleString('en-US')}
            {compact && <VisuallyHidden> {s.value.toLocaleString('en-US')} tokens</VisuallyHidden>}
            {(showPercent || compact) && (
              <Text span inherit>
                {' '}· {((s.value / total) * 100).toFixed(1)}%
              </Text>
            )}
          </Text>
        </Box>
      ))}
    </Box>
  )
}
