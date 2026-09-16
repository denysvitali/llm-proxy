import {
  Alert,
  Card,
  Divider,
  Group,
  Loader,
  Progress,
  SimpleGrid,
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
  const updated = usage ? new Date(usage.fetchedAt) : null
  const hasUpdated = updated !== null && !Number.isNaN(updated.getTime())

  return (
    <Card withBorder radius="lg" p="md" style={{ minWidth: 0 }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm" mb="sm">
        <div style={{ minWidth: 0 }}>
          <Title order={5}>ZCode plan</Title>
          <Text size="xs" c="dimmed">Current billing period · units</Text>
        </div>
        {query.isFetching && !query.isPending ? <Loader size="xs" aria-label="Refreshing ZCode usage" style={{ flexShrink: 0 }} /> : null}
      </Group>

      {error && (
        <Alert color="red" variant="light" title={usage ? 'Could not refresh usage' : 'Usage unavailable'} mb={usage ? 'sm' : 0}>
          {sanitizeZcodeError(error.message)}
          {usage && <Text size="sm" mt={4}>Showing the last available data.</Text>}
        </Alert>
      )}
      {query.isPending ? (
        <Group justify="center" py="md" role="status">
          <Loader size="sm" aria-hidden="true" />
          <Text size="sm" c="dimmed">Loading plan usage…</Text>
        </Group>
      ) : usage?.plans.length ? (
        <Stack gap="md">
          {usage.plans.map((plan, index) => (
            <div key={plan.plan_id}>
              {index > 0 && <Divider mb="md" />}
              <PlanRow plan={plan} />
            </div>
          ))}
          <Text size="xs" c="dimmed">
            {hasUpdated ? <>Updated <time dateTime={updated.toISOString()} title={updated.toLocaleString()}>{updated.toLocaleTimeString('en-US', { hour12: false })}</time></> : 'Update time unavailable'}
          </Text>
        </Stack>
      ) : usage || !error ? (
        <Text size="sm" c="dimmed">No plan usage is available for this account.</Text>
      ) : null}
    </Card>
  )
}

function PlanRow({ plan }: { plan: ZcodePlanUsage }) {
  // The API omits zero-valued fields, so a missing counter can be zero OR
  // unreported. Do not turn an absent used counter into a reassuring 0%.
  const total = validUnits(plan.total_units)
  const used = validUnits(plan.used_units)
  const available = validUnits(plan.available_units)
  const ratio = total !== null && total > 0 && used !== null ? (used / total) * 100 : null
  const percent = ratio !== null && Number.isFinite(ratio) ? ratio : null
  const color = usageTone(percent)
  const estimatedRemaining = available === null && total !== null && used !== null
    ? Math.max(total - used, 0)
    : null
  const remaining = available ?? estimatedRemaining
  const title = plan.name || plan.plan_id
  const status = [plan.status, plan.kind].filter(Boolean).join(' · ')
  const period = formatUnixPeriod(plan.period_start, plan.period_end)
  const description = percent === null ? '' : `${percent.toFixed(1)}% used${percent > 100 ? ', over plan limit' : ''}; ${used?.toLocaleString('en-US')} of ${total?.toLocaleString('en-US')} units used`

  return (
    <div style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      <Group justify="space-between" align="baseline" gap="xs" mb={8}>
        <div style={{ minWidth: 0 }}>
          <Text size="sm" fw={600}>{title}</Text>
          {status && <Text size="xs" c="dimmed">{status}</Text>}
        </div>
        {percent !== null && (
          <Text size="sm" fw={700}>
            {percent.toFixed(1)}% used
          </Text>
        )}
      </Group>
      {percent !== null ? (
        <>
          <Progress.Root
            size="md"
            radius="sm"
            role="meter"
            aria-label={`${title} quota used`}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.min(100, percent)}
            aria-valuetext={description}
            title={description}
            bg={`var(--mantine-color-${color}-light)`}
          >
            <Progress.Section value={Math.min(100, percent)} color={color} withAria={false} />
          </Progress.Root>
          {percent > 100 && <Text size="xs" mt={6}>{(percent - 100).toFixed(1)}% over plan limit</Text>}
        </>
      ) : (
        <Text size="sm" c="dimmed">
          {plan.reason || 'Usage percentage unavailable — quota totals or usage were not reported.'}
        </Text>
      )}
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="sm" verticalSpacing="xs" mt="sm">
        <QuotaValue label="Used units" value={used} />
        <QuotaValue label={estimatedRemaining !== null ? 'Remaining (estimated)' : 'Remaining units'} value={remaining} />
        <QuotaValue label="Total units" value={total} />
      </SimpleGrid>
      {estimatedRemaining !== null && (
        <Text size="xs" c="dimmed" mt={6}>Remaining is calculated from total minus used; it may exclude reserved units.</Text>
      )}
      {period && <Text size="xs" c="dimmed" mt={8}>{period}</Text>}
    </div>
  )
}

function validUnits(value?: number) {
  return value != null && Number.isFinite(value) && value >= 0 ? value : null
}

function QuotaValue({ label, value }: { label: string; value: number | null }) {
  return (
    <div style={{ minWidth: 0 }}>
      <Text size="xs" c="dimmed">{label}</Text>
      <Text size="sm" fw={value === null ? 400 : 600} title={value === null ? undefined : `${value.toLocaleString('en-US')} units`}>
        {value === null ? 'Not reported' : fmtInt(value)}
      </Text>
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
