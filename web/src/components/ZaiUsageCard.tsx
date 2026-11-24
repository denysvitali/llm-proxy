import { Box, Group, Progress, Stack, Text } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { ZaiUsage, ZaiWindow } from '../api'
import UsageCardShell from './UsageCardShell'

export default function ZaiUsageCard({ query }: { query: UseQueryResult<ZaiUsage, Error> }) {
  const usage = query.data
  return (
    <UsageCardShell title="Z.ai" ariaLabel="Z.ai GLM Coding Plan" subtitle={planLabel(usage)}
      isFetching={query.isFetching} isPending={query.isPending} hasData={!!usage} error={query.error} updated={usage?.fetchedAt}>
      {usage ? <Stack gap="sm">
        {usage.windows.length ? usage.windows.map((window, i) => <QuotaWindow key={`${window.kind}-${window.durationSeconds ?? i}`} window={window} />)
          : <Text size="sm" component="div" className="usage-card-empty">No Coding Plan quota reported.</Text>}
      </Stack> : null}
    </UsageCardShell>
  )
}

export function ZaiUsageCompact({ usage }: { usage: ZaiUsage }) {
  return <Stack gap={6} miw={0}>
    {usage.windows.length ? usage.windows.map((window, i) => (
      <Group key={`${window.kind}-${window.durationSeconds ?? i}`} justify="space-between" gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed">{windowLabel(window)}</Text>
        <Text size="sm" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{remainingLabel(window)}</Text>
      </Group>
    )) : <Text size="xs" c="dimmed">No Coding Plan quota</Text>}
  </Stack>
}

function QuotaWindow({ window }: { window: ZaiWindow }) {
  const remaining = remainingPercent(window)
  const color = remaining === null || remaining > 30 ? 'teal' : remaining > 10 ? 'yellow' : 'red'
  const reset = dateLabel(window.resetsAt)
  const label = windowLabel(window)
  const counts = countsLabel(window)
  const details = window.details?.filter((d) => d.usage > 0).map((d) => `${d.name} ${d.usage.toLocaleString()}`).join(' · ')
  return <Box>
    <Group justify="space-between" gap="xs" mb={5}>
      <Text size="xs" fw={500}>{label}</Text>
      <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{remainingLabel(window)}</Text>
    </Group>
    {remaining !== null && <Progress value={remaining} color={color} size="sm" radius="xl" aria-label={`${label} remaining`} aria-valuetext={`${remaining.toFixed(1)}% remaining`} />}
    {(counts || reset) && <Text size="xs" c="dimmed" mt={4} style={{ fontVariantNumeric: 'tabular-nums' }}>
      {counts}
      {counts && reset ? ' · ' : ''}
      {reset && <>Resets <time dateTime={window.resetsAt}>{reset}</time></>}
    </Text>}
    {details && <Text size="xs" c="dimmed" mt={2}>{details}</Text>}
  </Box>
}

function windowLabel(window: ZaiWindow) {
  const seconds = window.durationSeconds ?? 0
  const span = seconds === 7 * 86400 ? 'Weekly'
    : seconds === 30 * 86400 ? 'Monthly'
      : seconds === 86400 ? 'Daily'
        : seconds > 0 && seconds % 3600 === 0 ? `${seconds / 3600}-hour`
          : ''
  if (window.kind === 'tools') return span ? `${span} tool calls` : 'Tool calls'
  return span ? `${span} window` : 'Plan window'
}

function remainingPercent(window: ZaiWindow) {
  return Number.isFinite(window.usedPercent) ? Math.min(100, Math.max(0, 100 - window.usedPercent)) : null
}

function remainingLabel(window: ZaiWindow) {
  const remaining = remainingPercent(window)
  return remaining === null ? 'Not reported' : `${remaining.toLocaleString(undefined, { maximumFractionDigits: 1 })}% remaining`
}

function countsLabel(window: ZaiWindow) {
  if (window.used == null || window.limit == null) return ''
  return `${window.used.toLocaleString()} of ${window.limit.toLocaleString()} used`
}

function planLabel(usage?: ZaiUsage) {
  if (usage?.plan) return usage.plan
  if (usage?.level) return `GLM Coding ${usage.level.charAt(0).toUpperCase()}${usage.level.slice(1)}`
  return 'GLM Coding Plan usage'
}

function dateLabel(value?: string) {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toLocaleString()
}
