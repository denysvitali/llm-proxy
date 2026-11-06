import { Box } from '@mantine/core'
import type { CSSProperties } from 'react'
import { usageTone } from './usageFormatting'
import './usage-cards.css'

// A meter that speaks the same status vocabulary as the rest of the console:
// good / warning / critical come from the `--data-*` tokens, never from a
// one-off Mantine hue ramp. Data logic (percent, thresholds) is unchanged.
const toneToken: Record<string, string> = {
  teal: 'var(--data-good, #157748)',
  yellow: 'var(--data-warning, #946200)',
  red: 'var(--data-critical, #c33030)',
  blue: 'var(--muted, #71717a)',
}

export default function UsageMeter({ percent, compact = false, label }: { percent: number; compact?: boolean; label: string }) {
  const tone = usageTone(percent)
  const color = toneToken[tone] ?? toneToken.blue
  const description = `${percent.toFixed(1)}% used${percent > 100 ? ', over subscription limit' : `, ${(100 - percent).toFixed(1)}% remaining`}`
  return (
    <Box
      className="usage-meter"
      data-compact={compact || undefined}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.min(100, percent)}
      aria-valuetext={description}
      title={description}
      style={{ '--meter-color': color } as CSSProperties}
    >
      <span
        className="usage-meter-fill"
        aria-hidden="true"
        style={{ width: `${Math.min(100, percent)}%` }}
      />
    </Box>
  )
}
