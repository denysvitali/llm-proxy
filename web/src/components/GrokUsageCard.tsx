import {
  Box,
  Divider,
  Group,
  SimpleGrid,
  Text,
  ThemeIcon,
} from '@mantine/core'
import { IconCoin } from '@tabler/icons-react'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage } from '../api'
import UsageCardShell from './UsageCardShell'
import UsageMeter from './UsageMeter'
import { usagePercent, periodTypeLabel, formatPeriod } from './usageFormatting'

export default function GrokUsageCard({ query }: { query: UseQueryResult<GrokUsage, Error> }) {
  const usage = query.data
  const error = query.error
  const percent = usagePercent(usage)
  const moneyTiles = moneyAmounts(usage)
  const extra = extraUsage(usage)

  return (
    <UsageCardShell title="Grok subscription"
      subtitle={[usage?.subscriptionTier || 'xAI coding subscription', usage?.email].filter(Boolean).join(' · ')}
      isFetching={query.isFetching} isPending={query.isPending} hasData={!!usage}
      error={error ? new Error(sanitizeGrokError(error.message)) : null} updated={usage?.fetchedAt}>
      {usage ? (
        <>
          {percent !== null ? (
            <>
              <Group justify="space-between" align="baseline" gap="xs" mb={8}>
                <Box miw={0}>
                  {/* Display-size tracking mirrors the theme's SF heading rules; tabular figures keep the pair of percentages steady while polling. */}
                  <Text fz={{ base: 24, sm: 28 }} fw={700} lh={1.15} style={{ fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.02em' }}>
                    {percent.toFixed(1)}% <Text span fz="sm" fw={500} c="dimmed">used</Text>
                  </Text>
                  <Text size="xs" c="dimmed" mt={2}>{usedThisPeriodLabel(usage.periodType)}</Text>
                </Box>
                <Text size="sm" c="dimmed" style={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                  {percent > 100 ? `${(percent - 100).toFixed(1)}% over limit` : `${(100 - percent).toFixed(1)}% remaining`}
                </Text>
              </Group>
              <UsageMeter percent={percent} label="Grok subscription quota used" />
            </>
          ) : (
            <Text size="sm" c="dimmed">
              Usage percentage is unavailable. This does not mean the quota is unused.
            </Text>
          )}
          <Text size="xs" c="dimmed" mt={8}>{formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}</Text>
          {moneyTiles.length > 0 && (
            <>
              <Divider my="md" />
              <SimpleGrid cols={{ base: 2, sm: 2 }} spacing="sm" verticalSpacing="sm">
                {moneyTiles.map((tile) => (
                  <UsageMoney key={tile.label} label={tile.label} cents={tile.cents} />
                ))}
              </SimpleGrid>
            </>
          )}
          {extra && (
            <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mt="sm">
              <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
                <ThemeIcon variant="light" color="gray" size="sm" radius="md" aria-hidden="true" style={{ flexShrink: 0 }}>
                  <IconCoin size={14} stroke={1.8} />
                </ThemeIcon>
                <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
                  <Text size="xs" fw={500}>Extra usage</Text>
                  {usage.onDemandCapCents != null && Number.isFinite(usage.onDemandCapCents) && (
                    <Text size="xs" c="dimmed">{formatMoney(usage.onDemandCapCents)} limit</Text>
                  )}
                </Box>
              </Group>
              <Text size="xs" fw={600} style={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                {formatMoney(usage.onDemandUsedCents)} used
              </Text>
            </Group>
          )}
        </>
      ) : !error ? (
        <Text size="sm" c="dimmed">No billing data is available for this account.</Text>
      ) : null}
    </UsageCardShell>
  )
}

function UsageMoney({ label, cents }: { label: string; cents?: number }) {
  return (
    <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
      <Text fz={11} tt="uppercase" c="dimmed" fw={600} lh={1.3} style={{ letterSpacing: '0.06em' }}>{label}</Text>
      <Text fz={16} fw={700} lh={1.35} mt={2} style={{ fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.01em' }}>{formatMoney(cents)}</Text>
    </Box>
  )
}

function moneyAmounts(usage?: GrokUsage) {
  if (!usage) return []
  return [
    { label: 'Included limit', cents: usage.limitCents },
    { label: 'Used', cents: usage.usedCents },
    { label: 'Remaining', cents: usage.remainingCents },
    { label: 'Prepaid', cents: usage.prepaidCents },
  ].filter((tile) => tile.cents != null && Number.isFinite(tile.cents))
}

function extraUsage(usage?: GrokUsage) {
  if (!usage) return false
  return [usage.onDemandUsedCents, usage.onDemandCapCents].some((value) => value != null && Number.isFinite(value))
}

function formatMoney(cents?: number) {
  if (cents == null || !Number.isFinite(cents)) return '—'
  return (cents / 100).toLocaleString('en-US', { style: 'currency', currency: 'USD' })
}

function usedThisPeriodLabel(periodType?: string) {
  const label = periodTypeLabel(periodType).toLowerCase()
  if (label === 'weekly') return 'Used this week'
  if (label === 'monthly') return 'Used this month'
  return 'Used this period'
}

function sanitizeGrokError(message: string) {
  const decoded = message.startsWith('/api/grok/usage: ') ? message.slice('/api/grok/usage: '.length) : message
  return decoded || 'Usage information is temporarily unavailable.'
}
