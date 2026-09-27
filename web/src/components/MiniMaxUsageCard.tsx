import { Alert, Badge, Box, Button, Card, Divider, Group, Loader, Progress, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { useIsMutating, useMutation, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { claimMiniMaxCheckin, type MiniMaxCheckinPanel, type MiniMaxQuotaWindow, type MiniMaxUsage, type MiniMaxVideoQuota } from '../api'

export default function MiniMaxUsageCard({ query }: { query: UseQueryResult<MiniMaxUsage, Error> }) {
  const usage = query.data
  const account = usage?.account
  const queryClient = useQueryClient()
  const checkinPending = useIsMutating({ mutationKey: ['minimax-checkin'] }) > 0
  const checkin = useMutation({
    mutationKey: ['minimax-checkin'],
    mutationFn: claimMiniMaxCheckin,
    retry: false,
    onMutate: () => queryClient.cancelQueries({ queryKey: ['minimax-usage'] }),
    onSuccess: (result) => {
      queryClient.setQueryData<MiniMaxUsage>(['minimax-usage'], (current) => current ? {
        ...current, checkin: result.panel, checkinError: undefined,
      } : current)
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['minimax-usage'] }),
  })
  const panel = usage?.checkin
  const claimed = claimedToday(panel)
  const claimable = !claimed && !!panel?.days.some((day) => day.status === 2)
  const updated = dateLabel(usage?.fetchedAt)

  return (
    <Card component="section" aria-label="MiniMax Code account" withBorder radius="lg" p="md" miw={0} style={{ overflowWrap: 'anywhere' }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm" mb="sm">
        <Box miw={0}>
          <Title order={5}>MiniMax Code</Title>
          <Text size="xs" c="dimmed">{account?.tier || 'Account credits and quotas'}</Text>
        </Box>
        {query.isFetching && !query.isPending && <Loader size="xs" aria-label="Refreshing MiniMax usage" style={{ flexShrink: 0 }} />}
      </Group>
      <Stack gap="sm">
        {query.error && <Alert color="red" title={usage ? 'Could not refresh usage' : 'Usage unavailable'}>
          {query.error.message}
          {usage && <Text size="sm" mt={4}>Showing the last available data.</Text>}
        </Alert>}
        {query.isPending ? (
          <Group justify="center" py="md" role="status"><Loader size="sm" aria-hidden="true" /><Text size="sm" c="dimmed">Loading MiniMax account…</Text></Group>
        ) : usage ? <>
          {usage.accountError && <Alert color="yellow" title="Account unavailable">{usage.accountError}</Alert>}
          <Box>
            <Text size="xs" c="dimmed" fw={500}>Credit balance</Text>
            <Text fz={26} fw={700} lh={1.3} style={{ fontVariantNumeric: 'tabular-nums' }}>{creditLabel(account?.credit_balance)}</Text>
            {dateLabel(account?.expires_at_ms) && <Text size="xs" c="dimmed">Plan expires {dateLabel(account?.expires_at_ms)}</Text>}
          </Box>
          {account?.quota_state === 'not-subscribed' ? <Text size="sm" c="dimmed">No token plan subscription.</Text> : account?.quota_state === 'available' && account.quota ? (
            <Stack gap="sm">
              <QuotaWindow label="5-hour quota" quota={account.quota.five_hour} />
              <QuotaWindow label="Weekly quota" quota={account.quota.weekly} />
              {account.quota.video && <VideoQuota quota={account.quota.video} />}
            </Stack>
          ) : <Text size="sm" c="dimmed">Quota information is unavailable.</Text>}
          <Divider />
          <Group justify="space-between" gap="xs">
            <Box>
              <Title order={6}>Daily check-in</Title>
              <Text size="xs" c="dimmed">Claim today's credits for your account.</Text>
            </Box>
            <Button size="xs" mih={44} loading={checkinPending} disabled={!claimable || checkinPending || !!usage.checkinError || query.isError}
              onClick={() => checkin.mutate()}>{claimed ? 'Checked in today' : 'Check in'}</Button>
          </Group>
          {usage.checkinError && <Alert color="yellow" title="Check-in unavailable">{usage.checkinError}</Alert>}
          {checkin.error && <Alert color="red" title="Could not confirm check-in">
            {checkin.error.message}
            <Text size="sm" mt={4}>Check the refreshed status before trying again.</Text>
          </Alert>}
          {checkin.isSuccess && <Text size="sm" c="teal" role="status">
            {checkin.data.claim_result === 2 ? 'Already checked in today.' : `Checked in! +${checkin.data.points.toLocaleString()} credits.`}
            {checkin.data.claim_result === 1 && dateLabel(checkin.data.expire_at_ms) ? ` Credits expire ${dateLabel(checkin.data.expire_at_ms)}.` : ''}
          </Text>}
          {panel?.days.length ? <SimpleGrid type="container" cols={{ base: 3, '420px': 4, '640px': 7 }} spacing={5} verticalSpacing={5}>
            {[...panel.days].sort((a, b) => a.day_no - b.day_no).map((day) => (
              <Box key={day.day_no} p={6} style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 'var(--mantine-radius-sm)', background: day.is_today ? 'var(--mantine-color-blue-light)' : undefined }}>
                <Text size="xs" fw={day.is_today ? 700 : 500}>Day {day.day_no}</Text>
                <Text fz={11} fw={600} mt={4}>{Number.isFinite(day.points) ? day.points.toLocaleString() : '—'} credits</Text>
                {day.bonus_points != null && day.bonus_points > 0 && <Text fz={10} c="dimmed">Includes {day.bonus_points.toLocaleString()} bonus</Text>}
                <Text fz={10} c={day.status === 3 ? 'teal' : 'dimmed'} mt={4}>{day.status === 3 ? 'Claimed' : day.status === 2 ? 'Available' : day.status === 4 ? 'Unavailable' : 'Upcoming'}</Text>
                {day.is_today && <Text fz={10} fw={700}>Today</Text>}
              </Box>
            ))}
          </SimpleGrid> : <Text size="sm" c="dimmed">Check-in schedule is unavailable.</Text>}
          {!claimable && !claimed && !!panel?.days.length && !usage.checkinError && <Text size="xs" c="dimmed">Daily check-in is unavailable right now.</Text>}
          {updated && <Text size="xs" c="dimmed">Updated <time dateTime={usage.fetchedAt}>{updated}</time></Text>}
        </> : null}
      </Stack>
    </Card>
  )
}

export function MiniMaxUsageCompact({ usage }: { usage: MiniMaxUsage }) {
  return <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
    <Group justify="space-between" gap="xs">
      <Text size="xs" c="dimmed">Credit balance</Text>
      <Text size="sm" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{creditLabel(usage.account?.credit_balance)}</Text>
    </Group>
    <Group justify="space-between" gap="xs" mt={6}>
      <Text size="xs" c="dimmed">Daily check-in</Text>
      <Badge size="xs" variant="light" color={claimedToday(usage.checkin) ? 'teal' : 'gray'} tt="none">
        {usage.checkinError ? 'Unavailable' : claimedToday(usage.checkin) ? 'Checked in today' : usage.checkin?.days.some((day) => day.status === 2) ? 'Ready to claim' : 'Unavailable'}
      </Badge>
    </Group>
    {usage.accountError && <Text size="xs" c="dimmed" mt={4}>Account information unavailable</Text>}
  </Box>
}

function QuotaWindow({ label, quota }: { label: string; quota?: MiniMaxQuotaWindow }) {
  const value = quota?.remaining_percent
  const percent = value != null && Number.isFinite(value) && value >= 0 && value <= 100 ? value : null
  const color = percent === null || percent > 30 ? 'teal' : percent > 10 ? 'yellow' : 'red'
  const reset = dateLabel(quota?.reset_at_ms)
  return <Box>
    <Group justify="space-between" gap="xs" mb={5}>
      <Text size="xs" fw={500}>{label}</Text>
      <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>{quota?.unlimited ? 'Unlimited' : percent === null ? 'Not reported' : `${percent.toLocaleString(undefined, { maximumFractionDigits: 1 })}% remaining`}</Text>
    </Group>
    {!quota?.unlimited && percent !== null && <Progress value={percent} color={color} size="sm" radius="xl" aria-label={`${label} remaining`} aria-valuetext={`${percent}% remaining`} />}
    {reset && <Text size="xs" c="dimmed" mt={4}>Resets <time dateTime={new Date(quota!.reset_at_ms!).toISOString()}>{reset}</time></Text>}
  </Box>
}

function claimedToday(panel?: MiniMaxCheckinPanel) {
  return panel?.days.some((day) => day.is_today && day.status === 3) ?? false
}

function VideoQuota({ quota }: { quota: MiniMaxVideoQuota }) {
  const remaining = quota.remaining_count != null && Number.isFinite(quota.remaining_count) && quota.remaining_count >= 0 ? quota.remaining_count : null
  const total = quota.total_count != null && Number.isFinite(quota.total_count) && quota.total_count >= 0 ? quota.total_count : null
  const reset = dateLabel(quota.reset_at_ms)
  return <Box>
    <Group justify="space-between" gap="xs">
      <Text size="xs" fw={500}>Video quota</Text>
      <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {quota.unlimited ? 'Unlimited' : remaining === null ? 'Not reported' : `${remaining.toLocaleString()}${total === null ? '' : ` of ${total.toLocaleString()}`} remaining`}
      </Text>
    </Group>
    {reset && <Text size="xs" c="dimmed" mt={4}>Resets <time dateTime={new Date(quota.reset_at_ms!).toISOString()}>{reset}</time></Text>}
  </Box>
}

function creditLabel(balance?: string) {
  return typeof balance === 'string' && balance.trim() !== '' ? balance : 'Not reported'
}

function dateLabel(value?: number | string) {
  if (value == null || value === '' || value === 0) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toLocaleString()
}
