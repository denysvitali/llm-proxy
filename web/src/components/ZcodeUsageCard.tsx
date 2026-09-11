import {
  Alert,
  Card,
  Group,
  Loader,
  Progress,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { ZcodePlanUsage, ZcodeUsage } from '../api'
import { fmtInt } from '../format'
import { usageTone } from './GrokUsageCard'

export default function ZcodeUsageCard({ query }: { query: UseQueryResult<ZcodeUsage, Error> }) {
  const usage = query.data
  const error = query.error

  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb={10}>
        <div>
          <Title order={5}>ZCode plan</Title>
          <Text size="xs" c="dimmed">Current billing period</Text>
        </div>
        {query.isFetching ? <Loader size="xs" /> : null}
      </Group>

      {query.isPending ? (
        <Group justify="center" py="md"><Loader size="sm" /></Group>
      ) : error ? (
        <Alert color="red" variant="light" title="Usage unavailable">
          {sanitizeZcodeError(error.message)}
        </Alert>
      ) : !usage?.plans.length ? (
        <Text c="dimmed">No plan usage is available for this account.</Text>
      ) : (
        <Stack gap="md">
          {usage.plans.map((plan) => (
            <PlanRow key={plan.plan_id} plan={plan} />
          ))}
          <Text size="xs" c="dimmed">
            Updated {new Date(usage.fetchedAt).toLocaleTimeString('en-US', { hour12: false })}
          </Text>
        </Stack>
      )}
    </Card>
  )
}

function PlanRow({ plan }: { plan: ZcodePlanUsage }) {
  const total = plan.total_units ?? 0
  const used = plan.used_units ?? 0
  const available = plan.available_units
  const hasQuota = total > 0
  const percent = hasQuota ? Math.max(0, Math.min(100, (used / total) * 100)) : null
  const title = plan.name || plan.plan_id
  const status = [plan.status, plan.kind].filter(Boolean).join(' · ')

  return (
    <div>
      <Group justify="space-between" align="baseline" mb={4}>
        <div>
          <Text size="sm" fw={600}>{title}</Text>
          {status && <Text size="xs" c="dimmed">{status}</Text>}
        </div>
        {hasQuota && (
          <Text size="sm" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
            {percent?.toFixed(1)}%
          </Text>
        )}
      </Group>
      {hasQuota ? (
        <>
          <Progress value={percent ?? 0} color={usageTone(percent)} size="md" radius="sm" aria-label={`${title} used`} />
          <Group justify="space-between" mt={4}>
            <Text size="xs" c="dimmed">
              {fmtInt(used)} used · {fmtInt(available ?? Math.max(total - used, 0))} left
            </Text>
            <Text size="xs" c="dimmed">{formatUnixPeriod(plan.period_start, plan.period_end)}</Text>
          </Group>
        </>
      ) : (
        <Text size="sm" c="dimmed">{plan.reason || 'No active quota on this plan.'}</Text>
      )}
    </div>
  )
}

function formatUnixPeriod(start?: number, end?: number) {
  const startDate = unixDate(start)
  const endDate = unixDate(end)
  if (startDate && endDate) return `${startDate.toLocaleDateString()} – ${endDate.toLocaleDateString()}`
  if (endDate) return `Resets ${endDate.toLocaleString()}`
  if (startDate) return `Started ${startDate.toLocaleDateString()}`
  return ''
}

function unixDate(value?: number) {
  if (!value || !Number.isFinite(value)) return undefined
  const millis = value > 1e12 ? value : value * 1000
  const date = new Date(millis)
  return Number.isNaN(date.getTime()) ? undefined : date
}

function sanitizeZcodeError(message: string) {
  const decoded = message.startsWith('/api/zcode/usage: ') ? message.slice('/api/zcode/usage: '.length) : message
  return decoded || 'Usage information is temporarily unavailable.'
}
