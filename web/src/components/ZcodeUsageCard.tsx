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
import type { ZcodeBalance, ZcodePlanUsage, ZcodeQuota } from '../api'
import { fmtInt } from '../format'
import { usageTone } from './usageFormatting'
import UsageCardShell from './UsageCardShell'
import ZcodeOffers from './ZcodeOffers'

export default function ZcodeUsageCard({ query }: { query: UseQueryResult<ZcodeQuota, Error> }) {
  const usage = query.data
  const error = query.error

  return (
    <UsageCardShell title="ZCode plan" subtitle="Live account quota · units"
      isFetching={query.isFetching} isPending={query.isPending} hasData={!!usage}
      error={error ? new Error(sanitizeZcodeError(error.message)) : null} updated={usage?.fetchedAt}>
      {usage && (usage.plans.length > 0 || usage.balances.length > 0) ? (
        <Stack gap="md">
          {usage.plans.map((plan, index) => (
            <Box key={plan.user_plan_id || `${plan.plan_id}-${index}`}>
              {index > 0 && <Divider mb="md" />}
              <PlanRow plan={plan} />
            </Box>
          ))}
          {usage.balances.length > 0 && <Stack gap="md" aria-label="ZCode quota balances">
            {usage.plans.length > 0 && <Divider label="Quota balances" labelPosition="left" />}
            {usage.balances.map((balance, index) => (
              <Box key={`${balance.plan_id}-${balance.entitlement_id}-${balance.meter}-${index}`}>
                {index > 0 && <Divider mb="md" />}
                <BalanceRow balance={balance} plans={usage.plans} />
              </Box>
            ))}
          </Stack>}
          {usage.balances.length === 0 && <Text size="sm" c="dimmed">No quota balances are available for this account.</Text>}
        </Stack>
      ) : usage || !error ? (
        <Text size="sm" c="dimmed">No plan usage is available for this account.</Text>
      ) : null}
      <Box mt="md"><ZcodeOffers /></Box>
    </UsageCardShell>
  )
}

function PlanRow({ plan }: { plan: ZcodePlanUsage }) {
  const period = formatUnixPeriod(plan.starts_at ?? plan.period_start, plan.ends_at ?? plan.period_end)
  const status = [plan.status, plan.kind].filter(Boolean).join(' · ')
  return (
    <Box style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      <Text size="sm" fw={600}>{plan.name || plan.plan_id}</Text>
      {status && <Text size="xs" c="dimmed">{status}</Text>}
      {plan.reason && <Text size="xs" c="dimmed" mt={4}>{plan.reason}</Text>}
      {period && <Text size="xs" c="dimmed" mt={4}>{period}</Text>}
      {!!plan.entitlements?.length && <Stack gap={4} mt={6}>
        {plan.entitlements.map((entitlement, index) => <Text size="xs" c="dimmed" key={`${entitlement.entitlement_id}-${index}`}>
          {[entitlement.show_name || entitlement.entitlement_id, entitlement.period].filter(Boolean).join(' · ')}
        </Text>)}
      </Stack>}
    </Box>
  )
}

function BalanceRow({ balance, plans }: { balance: ZcodeBalance; plans: ZcodePlanUsage[] }) {
  // Reported zero counters stay zero; absent counters remain unknown.
  // Do not turn an absent used counter into a reassuring 0%.
  const total = validUnits(balance.total_units)
  const used = validUnits(balance.used_units)
  const ratio = total !== null && total > 0 && used !== null ? (used / total) * 100 : null
  const percent = ratio !== null && Number.isFinite(ratio) ? ratio : null
  const color = usageTone(percent)
  const plan = balance.plan_id ? plans.find((candidate) => candidate.plan_id === balance.plan_id || candidate.user_plan_id === balance.plan_id) : undefined
  const entitlement = plan?.entitlements?.find((candidate) => candidate.entitlement_id === balance.entitlement_id)
  const title = balance.show_name || entitlement?.show_name || balance.entitlement_id || 'Quota balance'
  const status = [plan?.name, balance.meter, ...balance.capabilities].filter(Boolean).join(' · ')
  const expires = unixDate(balance.expires_at)
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
            <Text size="xs" mt={6} fw={500} style={{ color: 'var(--data-critical)', fontVariantNumeric: 'tabular-nums' }}>
              {(percent - 100).toFixed(1)}% over plan limit
            </Text>
          )}
        </>
      ) : null}
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="sm" verticalSpacing="xs" mt="sm">
        <QuotaValue label="Used units" value={used} />
        <QuotaValue label="Remaining units" value={validUnits(balance.remaining_units)} />
        <QuotaValue label="Available units" value={validUnits(balance.available_units)} />
        <QuotaValue label="Reserved units" value={validUnits(balance.reserved_units)} />
        <QuotaValue label="Total units" value={total} />
      </SimpleGrid>
      <Text size="xs" c="dimmed" mt={8}>{expires ? <>Expires <time dateTime={expires.toISOString()}>{expires.toLocaleString()}</time></> : 'Expiry not reported'}</Text>
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
  if (endDate) return `Ends ${endDate.toLocaleString()}`
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
  const decoded = message.replace(/^\/api\/zcode\/(?:usage|quota): /, '')
  return decoded || 'Usage information is temporarily unavailable.'
}
