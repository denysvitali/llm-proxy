import { Box, Group, Progress, Stack, Text } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { OpenDesignUsage, OpenDesignWindow } from '../api'
import UsageCardShell from './UsageCardShell'

export default function OpenDesignUsageCard({ query }: { query: UseQueryResult<OpenDesignUsage, Error> }) {
  const usage = query.data
  return (
    <UsageCardShell title="Open Design" ariaLabel="Open Design Coding Plan" subtitle={tierLabel(usage?.tier)}
      isFetching={query.isFetching} isPending={query.isPending} hasData={!!usage} error={query.error} updated={usage?.fetchedAt}>
      {usage ? <Stack gap="sm">
        <Box className="usage-card-balance">
          <Text size="xs" c="dimmed" fw={500}>Wallet balance</Text>
          <Text fz={26} fw={700} lh={1.3} style={{ fontVariantNumeric: 'tabular-nums' }}>{balanceLabel(usage.balanceUsd)}</Text>
          {usage.funding && <Text size="xs" c="dimmed">Requests funded by {fundingLabel(usage.funding)}</Text>}
        </Box>
        {usage.windows.length ? usage.windows.map((window) => <PlanWindow key={`${window.label}-${window.durationSeconds}`} window={window} />)
          : <Text size="sm" component="div" className="usage-card-empty">{usage.eligible ? 'No Coding Plan windows reported.' : 'No Coding Plan on this workspace.'}</Text>}
      </Stack> : null}
    </UsageCardShell>
  )
}

export function OpenDesignUsageCompact({ usage }: { usage: OpenDesignUsage }) {
  return <Stack gap={6} miw={0}>
    {usage.windows.length ? usage.windows.map((window) => (
      <Group key={`${window.label}-${window.durationSeconds}`} justify="space-between" gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed">{window.label}</Text>
        <Text size="sm" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{remainingLabel(window)}</Text>
      </Group>
    )) : <Text size="xs" c="dimmed">No Coding Plan windows</Text>}
  </Stack>
}

function PlanWindow({ window }: { window: OpenDesignWindow }) {
  const remaining = finite(window.remainingPercent) ? Math.min(100, Math.max(0, window.remainingPercent)) : null
  const color = remaining === null || remaining > 30 ? 'teal' : remaining > 10 ? 'yellow' : 'red'
  const reset = dateLabel(window.resetsAt)
  const label = `${window.label} window`
  return <Box>
    <Group justify="space-between" gap="xs" mb={5}>
      <Text size="xs" fw={500}>{label}</Text>
      <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{remainingLabel(window)}</Text>
    </Group>
    {remaining !== null && <Progress value={remaining} color={color} size="sm" radius="xl" aria-label={`${label} remaining`} aria-valuetext={`${remaining.toFixed(1)}% remaining`} />}
    <Text size="xs" c="dimmed" mt={4} style={{ fontVariantNumeric: 'tabular-nums' }}>
      {amountLabel(window)}
      {reset ? <> · Resets <time dateTime={window.resetsAt}>{reset}</time></> : window.resetMode === 'activity_triggered' ? ' · Starts with your next request' : ''}
    </Text>
  </Box>
}

function remainingLabel(window: OpenDesignWindow) {
  if (finite(window.usedPercent) && window.usedPercent > 100) return `Over limit (${fmtPercent(window.usedPercent)} used)`
  return finite(window.remainingPercent) ? `${fmtPercent(window.remainingPercent)} remaining` : 'Not reported'
}

function amountLabel(window: OpenDesignWindow) {
  if (finite(window.usedUsd) && finite(window.limitUsd)) return `${usd(window.usedUsd)} of ${usd(window.limitUsd)} used`
  return `${window.usedCredits.toLocaleString()} of ${window.limitCredits.toLocaleString()} credits used`
}

function finite(value?: number): value is number {
  return value != null && Number.isFinite(value)
}

function fmtPercent(value: number) {
  return `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}%`
}

function usd(value: number) {
  // Plan amounts are small; four decimals keep sub-cent usage visible.
  return value.toLocaleString(undefined, { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: value !== 0 && Math.abs(value) < 1 ? 4 : 2 })
}

function balanceLabel(balance?: string) {
  const value = balance == null || balance.trim() === '' ? Number.NaN : Number(balance)
  return Number.isFinite(value) ? usd(value) : 'Not reported'
}

function tierLabel(tier?: string) {
  return tier ? `${tier.charAt(0).toUpperCase()}${tier.slice(1)} Coding Plan` : 'Coding Plan usage'
}

function fundingLabel(funding: string) {
  return funding === 'coding_plan' ? 'the Coding Plan' : funding === 'wallet' ? 'the wallet' : funding
}

function dateLabel(value?: string) {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toLocaleString()
}
