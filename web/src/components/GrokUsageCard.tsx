import {
  Alert,
  Box,
  Card,
  Divider,
  Group,
  Loader,
  Progress,
  SimpleGrid,
  Text,
  ThemeIcon,
  Title,
} from '@mantine/core'
import { IconCoin } from '@tabler/icons-react'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage } from '../api'

export default function GrokUsageCard({ query }: { query: UseQueryResult<GrokUsage, Error> }) {
  const usage = query.data
  const error = query.error
  const percent = usagePercent(usage)
  const moneyTiles = moneyAmounts(usage)
  const extra = extraUsage(usage)
  const updated = usage ? new Date(usage.fetchedAt) : null
  const hasUpdated = updated !== null && !Number.isNaN(updated.getTime())

  return (
    <Card withBorder radius="lg" p="md" style={{ minWidth: 0 }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm" mb="sm">
        <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
          <Title order={5}>Grok subscription</Title>
          <Text size="xs" c="dimmed">
            {usage?.subscriptionTier || 'xAI coding subscription'}
            {usage?.email ? ` · ${usage.email}` : ''}
          </Text>
        </Box>
        {query.isFetching && !query.isPending ? <Loader size="xs" aria-label="Refreshing Grok usage" style={{ flexShrink: 0 }} /> : null}
      </Group>

      {error && (
        <Alert color="red" variant="light" title={usage ? 'Could not refresh usage' : 'Usage unavailable'} mb={usage ? 'sm' : 0}>
          {sanitizeGrokError(error.message)}
          {usage && <Text size="sm" mt={4}>Showing the last available data.</Text>}
        </Alert>
      )}
      {query.isPending ? (
        <Group justify="center" py="md" role="status">
          <Loader size="sm" aria-hidden="true" />
          <Text size="sm" c="dimmed">Loading subscription usage…</Text>
        </Group>
      ) : usage ? (
        <>
          {percent !== null ? (
            <>
              <Group justify="space-between" align="baseline" wrap="nowrap" gap="xs" mb={8}>
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
              <UsageMeter percent={percent} />
            </>
          ) : (
            <Text size="sm" c="dimmed">
              Usage percentage is unavailable. This does not mean the quota is unused.
            </Text>
          )}
          <Group justify="space-between" align="baseline" gap="xs" mt={8} wrap="nowrap">
            <Text size="xs" c="dimmed" miw={0} style={{ overflowWrap: 'anywhere' }}>{formatPeriod(usage.periodStart, usage.periodEnd, usage.periodType)}</Text>
            <Text size="xs" c="dimmed" style={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
              {hasUpdated ? <>Updated <time dateTime={updated.toISOString()} title={updated.toLocaleString()}>{updated.toLocaleTimeString('en-US', { hour12: false })}</time></> : 'Update time unavailable'}
            </Text>
          </Group>
          {moneyTiles.length > 0 && (
            <>
              <Divider my="md" />
              <SimpleGrid cols={{ base: 2, lg: moneyTiles.length }} spacing="sm" verticalSpacing="sm">
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
    </Card>
  )
}

export function GrokUsageCompact({ usage }: { usage: GrokUsage }) {
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
      // Same-ramp track: tinted version of the state color behind the fill.
      bg={`var(--mantine-color-${color}-light)`}
    >
      <Progress.Section value={Math.min(100, percent)} color={color} withAria={false} />
    </Progress.Root>
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

export function usageTone(percent: number | null) {
  if (percent === null) return 'blue'
  if (percent >= 90) return 'red'
  if (percent >= 70) return 'yellow'
  return 'teal'
}

function formatMoney(cents?: number) {
  if (cents == null || !Number.isFinite(cents)) return '—'
  return (cents / 100).toLocaleString('en-US', { style: 'currency', currency: 'USD' })
}

export function periodTypeLabel(periodType?: string) {
  if (!periodType) return ''
  const trimmed = periodType.replace(/^USAGE_PERIOD_TYPE_/i, '').replace(/_/g, ' ').toLowerCase()
  return trimmed ? trimmed.replace(/^\w/, (ch) => ch.toUpperCase()) : ''
}

function usedThisPeriodLabel(periodType?: string) {
  const label = periodTypeLabel(periodType).toLowerCase()
  if (label === 'weekly') return 'Used this week'
  if (label === 'monthly') return 'Used this month'
  return 'Used this period'
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
