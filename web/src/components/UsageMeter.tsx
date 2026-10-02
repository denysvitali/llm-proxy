import { Progress } from '@mantine/core'
import { usageTone } from './usageFormatting'

export default function UsageMeter({ percent, compact = false }: { percent: number; compact?: boolean }) {
  const color = usageTone(percent)
  const description = `${percent.toFixed(1)}% used${percent > 100 ? ', over subscription limit' : `, ${(100 - percent).toFixed(1)}% remaining`}`
  return (
    <Progress.Root
      size={compact ? 'sm' : 'md'}
      radius="xl"
      role="meter"
      aria-label="Grok subscription quota used"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.min(100, percent)}
      aria-valuetext={description}
      title={description}
      // Same-ramp track: tinted version of the state color behind the fill.
      bg={`var(--mantine-color-${color}-light)`}
    >
      <Progress.Section value={Math.min(100, percent)} color={color} withAria={false} />
    </Progress.Root>
  )
}
