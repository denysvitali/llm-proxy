import {
  Box,
  Divider,
  Group,
  Progress,
  SimpleGrid,
  Stack,
  Text,
} from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { ZcodePlanUsage, ZcodeUsage } from '../api'
import { fmtInt } from '../format'
import { usageTone } from './usageFormatting'
import UsageCardShell from './UsageCardShell'

export default function ZcodeUsageCard({ query }: { query: UseQueryResult<ZcodeUsage, Error> }) {
  const usage = query.data
  const error = query.error

  return (
    <UsageCardShell title="ZCode plan" subtitle="Current billing period · units"
      isFetching={query.isFetching} isPending={query.isPending} hasData={!!usage}
      error={error ? new Error(sanitizeZcodeError(error.message)) : null} updated={usage?.fetchedAt}>
      {usage?.plans.length ? (
        <Stack gap="md">
          {usage.plans.map((plan, index) => (
            <Box key={plan.plan_id}>
              {index > 0 && <Divider mb="md" />}
              <PlanRow plan={plan} />
            </Box>
          ))}
        </Stack>
      ) : usage || !error ? (
        <Text size="sm" c="dimmed">No plan usage is available for this account.</Text>
      ) : null}
    </UsageCardShell>
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
    <Box style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      <Group justify="space-between" align="baseline" gap="xs" mb={8}>
        <Box style={{ minWidth: 0 }}>
          <Text size="sm" fw={600}>{title}</Text>
          {status && <Text size="xs" c="dimmed">{status}</Text>}
        </Box>
        {percent !== null && (
          <Text size="sm" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
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
          {percent > 100 && (
            <Text size="xs" mt={6} c="red" fw={500} style={{ fontVariantNumeric: 'tabular-nums' }}>
              {(percent - 100).toFixed(1)}% over plan limit
            </Text>
          )}
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
    </Box>
  )
}

function validUnits(value?: number) {
  return value != null && Number.isFinite(value) && value >= 0 ? value : null
}

function QuotaValue({ label, value }: { label: string; value: number | null }) {
  return (
    <Box style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      <Text size="xs" c="dimmed" fw={500}>{label}</Text>
      <Text
        size="sm"
        fw={value === null ? 400 : 600}
        c={value === null ? 'dimmed' : undefined}
        title={value === null ? 'The API did not report this counter' : `${value.toLocaleString('en-US')} units`}
        style={{ fontVariantNumeric: 'tabular-nums' }}
      >
        {value === null ? 'Not reported' : fmtInt(value)}
      </Text>
    </Box>
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
  // Accept contemporary epochs in either seconds or milliseconds.
  const millis = value > 1e12 ? value : value * 1000
  const date = new Date(millis)
  return Number.isNaN(date.getTime()) ? undefined : date
}

function sanitizeZcodeError(message: string) {
  const decoded = message.startsWith('/api/zcode/usage: ') ? message.slice('/api/zcode/usage: '.length) : message
  return decoded || 'Usage information is temporarily unavailable.'
}
