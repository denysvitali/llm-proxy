import { Text } from '@mantine/core'
import { clampRate } from '../../format'
import { observedRate } from './modelData'

// A thin inline meter plus the exact percentage. Unknown observations (no
// requests yet) stay an em dash, never a 0% bar; a real 0% stays a visibly
// empty track with "0.0%", so "nothing measured" and "everything failed" cannot
// be confused (DESIGN.md "Unknown versus zero").
//
// Tone thresholds are deliberately wider than UptimeBadge's: a card is a scan
// surface, so only a real problem earns a status hue. >=99% is good, >=90% is
// neutral (no hue at all), >=50% warning, below that critical.
type Tone = 'good' | 'neutral' | 'warning' | 'critical'

function toneFor(ratio: number): Tone {
  if (ratio >= 0.99) return 'good'
  if (ratio >= 0.9) return 'neutral'
  if (ratio >= 0.5) return 'warning'
  return 'critical'
}

export function SuccessMeter({
  rate,
  requests,
  compact = false,
}: {
  rate: number
  requests: number
  compact?: boolean
}) {
  if (!Number.isFinite(requests) || requests <= 0 || !Number.isFinite(rate)) {
    return (
      <Text size="sm" c="dimmed">
        —
      </Text>
    )
  }
  const ratio = clampRate(rate)
  return (
    <span className="catalog-success" data-compact={compact ? 'true' : undefined}>
      <span className="catalog-success-track" aria-hidden="true">
        <span data-tone={toneFor(ratio)} style={{ width: `${Math.max(ratio * 100, ratio > 0 ? 2 : 0)}%` }} />
      </span>
      <span className="catalog-success-pct tabular">{observedRate(rate, requests)}</span>
    </span>
  )
}
