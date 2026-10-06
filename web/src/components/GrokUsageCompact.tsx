import { Group, Text } from '@mantine/core'
import type { GrokUsage } from '../api'
import UsageMeter from './UsageMeter'
import { usagePercent, periodTypeLabel, formatPeriod } from './usageFormatting'

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
      {percent !== null && <UsageMeter percent={percent} compact label="Grok subscription quota used" />}
      <Text size="xs" c="dimmed" mt={4}>
        {formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}
      </Text>
    </div>
  )
}
