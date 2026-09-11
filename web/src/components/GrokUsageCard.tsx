import {
  Alert,
  Card,
  Divider,
  Group,
  Loader,
  Progress,
  SimpleGrid,
  Text,
  Title,
} from '@mantine/core'
import { IconCoin } from '@tabler/icons-react'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage } from '../api'

export default function GrokUsageCard({ query }: { query: UseQueryResult<GrokUsage, Error> }) {
  const usage = query.data
  const error = query.error
  const percent = usage?.hasPercent ? Math.max(0, Math.min(100, usage.percentUsed)) : null
  const color = usageTone(percent)
  const moneyTiles = moneyAmounts(usage)
  const hasMoney = moneyTiles.length > 0
  const extra = extraUsage(usage)

  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb={usage?.hasPercent ? 10 : 0}>
        <div>
          <Title order={5}>Grok subscription</Title>
          <Text size="xs" c="dimmed">
            {usage?.subscriptionTier || 'xAI coding subscription'}
            {usage?.email ? ` · ${usage.email}` : ''}
          </Text>
        </div>
        {query.isFetching ? <Loader size="xs" /> : null}
      </Group>

      {query.isPending ? (
        <Group justify="center" py="md"><Loader size="sm" /></Group>
      ) : error ? (
        <Alert color="red" variant="light" title="Usage unavailable">
          {sanitizeGrokError(error.message)}
        </Alert>
      ) : !usage?.hasPercent ? (
        <Text c="dimmed">No billing data is available for this account.</Text>
      ) : (
        <>
          <Group justify="space-between" align="baseline" mb={6}>
            <Text fz={28} fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
              {usage.percentUsed.toFixed(1)}%
            </Text>
            <Text size="sm" c="dimmed">{usedThisPeriodLabel(usage.periodType)}</Text>
          </Group>
          <Progress value={percent ?? 0} color={color} size="lg" radius="sm" aria-label="Grok subscription used" />
          <Group justify="space-between" mt={6}>
            <Text size="xs" c="dimmed">{formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}</Text>
            <Text size="xs" c="dimmed">Updated {new Date(usage.fetchedAt).toLocaleTimeString('en-US', { hour12: false })}</Text>
          </Group>
          {hasMoney && (
            <>
              <Divider my="md" />
              <SimpleGrid cols={{ base: 2, sm: moneyTiles.length }} spacing="md">
                {moneyTiles.map((tile) => (
                  <UsageMoney key={tile.label} label={tile.label} cents={tile.cents} />
                ))}
              </SimpleGrid>
            </>
          )}
          {extra && (
            <Group gap={6} mt="sm">
              <IconCoin size={15} stroke={1.8} />
              <Text size="xs" c="dimmed">
                Extra usage {formatMoney(usage.onDemandUsedCents)}
                {usage.onDemandCapCents != null ? ` of ${formatMoney(usage.onDemandCapCents)}` : ''}
              </Text>
            </Group>
          )}
        </>
      )}
    </Card>
  )
}

export function GrokUsageCompact({ usage }: { usage: GrokUsage }) {
  if (!usage.hasPercent) return null
  const percent = Math.max(0, Math.min(100, usage.percentUsed))
  return (
    <div>
      <Group justify="space-between" mb={4}>
        <Text size="xs" c="dimmed">
          {usage.subscriptionTier || 'Grok'} · {periodTypeLabel(usage.periodType) || 'quota'}
        </Text>
        <Text size="xs" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
          {usage.percentUsed.toFixed(1)}%
        </Text>
      </Group>
      <Progress value={percent} color={usageTone(percent)} size="sm" radius="sm" aria-label="Grok subscription used" />
      <Text size="xs" c="dimmed" mt={4}>
        {formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}
      </Text>
    </div>
  )
}

function UsageMoney({ label, cents }: { label: string; cents?: number }) {
  return (
    <div>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>{label}</Text>
      <Text fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMoney(cents)}</Text>
    </div>
  )
}

function moneyAmounts(usage?: GrokUsage) {
  if (!usage) return []
  return [
    { label: 'Included limit', cents: usage.limitCents },
    { label: 'Used', cents: usage.usedCents },
    { label: 'Remaining', cents: usage.remainingCents },
    { label: 'Prepaid', cents: usage.prepaidCents },
  ].filter((tile) => tile.cents != null && Number.isFinite(tile.cents) && tile.cents !== 0)
}

function extraUsage(usage?: GrokUsage) {
  if (!usage) return false
  const used = usage.onDemandUsedCents
  const cap = usage.onDemandCapCents
  return (used != null && used !== 0) || (cap != null && cap > 0)
}

export function usageTone(percent: number | null) {
  if (percent === null) return 'blue'
  if (percent >= 90) return 'red'
  if (percent >= 70) return 'yellow'
  return 'teal'
}

function formatMoney(cents?: number) {
  if (cents == null || !Number.isFinite(cents)) return '—'
  return (Math.abs(cents) / 100).toLocaleString('en-US', { style: 'currency', currency: 'USD' })
}

export function periodTypeLabel(periodType?: string) {
  if (!periodType) return ''
  const trimmed = periodType.replace(/^USAGE_PERIOD_TYPE_/i, '').replace(/_/g, ' ').toLowerCase()
  return trimmed ? trimmed.replace(/^\w/, (ch) => ch.toUpperCase()) : ''
}

function usedThisPeriodLabel(periodType?: string) {
  const label = periodTypeLabel(periodType).toLowerCase()
  if (label === 'weekly') return 'used this week'
  if (label === 'monthly') return 'used this month'
  return 'used this period'
}

export function formatPeriod(start?: string, end?: string, periodType?: string) {
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

function sanitizeGrokError(message: string) {
  const decoded = message.startsWith('/api/grok/usage: ') ? message.slice('/api/grok/usage: '.length) : message
  return decoded || 'Usage information is temporarily unavailable.'
}
