import { Group, Progress, Text } from '@mantine/core'
import type { GrokUsage } from '../api'

function usagePercent(usage?: GrokUsage) {
  return usage?.hasPercent && Number.isFinite(usage.percentUsed) && usage.percentUsed >= 0
    ? usage.percentUsed
    : null
}

function UsageMeter({ percent, compact = false }: { percent: number; compact?: boolean }) {
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
      bg={`var(--mantine-color-${color}-light)`}
    >
      <Progress.Section value={Math.min(100, percent)} color={color} withAria={false} />
    </Progress.Root>
  )
}

function usageTone(percent: number | null) {
  if (percent === null) return 'blue'
  if (percent >= 90) return 'red'
  if (percent >= 70) return 'yellow'
  return 'teal'
}

function periodTypeLabel(periodType?: string) {
  if (!periodType) return ''
  const trimmed = periodType.replace(/^USAGE_PERIOD_TYPE_/i, '').replace(/_/g, ' ').toLowerCase()
  return trimmed ? trimmed.replace(/^\w/, (ch) => ch.toUpperCase()) : ''
}

function formatPeriod(start?: string, end?: string, periodType?: string) {
  const kind = periodTypeLabel(periodType)
  if (!start && !end) return kind || 'Current period'
  const startDate = start ? new Date(start) : undefined
  const endDate = end ? new Date(end) : undefined
  const validStart = startDate && !Number.isNaN(startDate.getTime())
  const validEnd = endDate && !Number.isNaN(endDate.getTime())
  const prefix = kind ? `${kind} · ` : ''
  if (validStart && validEnd) return `${prefix}${startDate!.toLocaleDateString()} – ${endDate!.toLocaleDateString()}`
  if (validEnd) return `${prefix}Resets ${endDate!.toLocaleString()}`
  if (validStart) return `${prefix}Started ${startDate!.toLocaleDateString()}`
  return kind || 'Current period'
}

export default function GrokUsageCompact({ usage }: { usage: GrokUsage }) {
  const percent = usagePercent(usage)
  return (
    <div style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      <Group justify="space-between" align="baseline" gap="xs" mb={6}>
        <Text size="xs" c="dimmed" miw={0}>
          {usage.subscriptionTier || 'Grok'} · {periodTypeLabel(usage.periodType) || 'quota'}
        </Text>
        <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
          {percent === null ? 'Usage unavailable' : `${percent.toFixed(1)}% used`}
        </Text>
      </Group>
      {percent !== null && <UsageMeter percent={percent} compact />}
      <Text size="xs" c="dimmed" mt={4}>
        {formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}
      </Text>
    </div>
  )
}
