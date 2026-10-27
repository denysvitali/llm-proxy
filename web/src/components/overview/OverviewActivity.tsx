import { useState } from 'react'
import { Alert, Badge, Button, Card, Group, Loader, Modal, ScrollArea, Stack, Table, Text, ThemeIcon, Title, Tooltip } from '@mantine/core'
import { IconAlertTriangle, IconCheck, IconClock, IconListDetails, IconPointFilled, IconServerOff, IconShieldCheck } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { ApiError, fetchRequest, type InspectedRequest, type UpstreamErrorEvent } from '../../api'
import { fmtInt } from '../../format'
import { EmptyState } from '../EmptyState'
import { statusSeverity, severityColor, statusDescription } from '../../lib/httpStatus'

// A captured attempt is treated as freshly landed (and highlighted) when the
// event arrived moments ago; the highlight fades naturally as time passes.
const FRESH_WINDOW_MS = 15_000

function isFresh(at: string) {
  const parsed = Date.parse(at)
  return !Number.isNaN(parsed) && Date.now() - parsed < FRESH_WINDOW_MS
}

// Compact timestamp: same-day events show clock time, older ones get a
// short date + time; the tooltip always carries the full locale timestamp
// so the exact moment is never hidden by the compact form.
function EventTime({ at }: { at: string }) {
  const parsed = new Date(at)
  if (Number.isNaN(parsed.getTime())) {
    return <Text size="xs" c="dimmed" style={{ fontVariantNumeric: 'tabular-nums' }}>{at}</Text>
  }
  const sameDay = new Date().toDateString() === parsed.toDateString()
  const primary = sameDay
    ? parsed.toLocaleTimeString('en-US', { hour12: false })
    : parsed.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
  return (
    <Tooltip label={parsed.toLocaleString('en-US')} events={{ hover: true, focus: true, touch: true }}>
      <Text
        component="time"
        dateTime={at}
        tabIndex={0}
        size="xs"
        c="dimmed"
        style={{ fontVariantNumeric: 'tabular-nums', cursor: 'default', borderRadius: 4 }}
      >
        {primary}
      </Text>
    </Tooltip>
  )
}

const wireLabels: Record<string, string> = {
  anthropic: 'Anthropic',
  'openai-chat': 'Chat',
  'openai-responses': 'Responses',
}

// The upstream wire format the attempt used; an unrecorded kind is unknown.
function WireLabel({ kind }: { kind?: string }) {
  if (!kind) return <Text size="xs" c="dimmed" aria-label="Wire format unknown">—</Text>
  return <Text size="xs" c="dimmed" title={kind}>{wireLabels[kind] ?? kind}</Text>
}

// UpstreamErrorsCard lists the most recent upstream failures newest-first:
// when it happened, which backend/model, the HTTP status (or "no response"),
// and what the upstream said about it.
export function UpstreamErrorsCard({ errors }: { errors: UpstreamErrorEvent[] }) {
  const shown = errors.slice(0, 6)
  return (
    <Card p="md" miw={0} className="activity-card">
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mb="sm">
        <Group gap={8} wrap="nowrap" miw={0}>
          <ThemeIcon variant="light" size="sm" radius="md" aria-hidden="true"
            styles={{ root: { color: 'var(--data-critical)', background: 'color-mix(in srgb, var(--data-critical) 12%, var(--card))' } }}
          >
            <IconServerOff size={13} />
          </ThemeIcon>
          <Title order={5} fz={13} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>Recent upstream errors</Title>
        </Group>
        <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
          {errors.length === 1 ? '1 failure' : `${fmtInt(errors.length)} failures · newest first`}
        </Text>
      </Group>
      <ScrollArea className="activity-feed" type="auto" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Recent upstream errors' }}>
      {errors.length === 0 ? (
        <EmptyState icon={<IconShieldCheck size={20} />} title="No recent upstream errors"
          hint="No failures are present in the retained error history. This is separate from the chart time range." />
      ) : (
        <Table className="upstream-errors-table" verticalSpacing="xs" horizontalSpacing="sm">
          <colgroup><col style={{ width: '26%' }} /><col /><col style={{ width: '24%' }} /><col style={{ width: '27%' }} /></colgroup>
          <Table.Thead>
            <Table.Tr>
              <Table.Th scope="col">Time</Table.Th>
              <Table.Th scope="col">Backend / model</Table.Th>
              <Table.Th scope="col">Status</Table.Th>
              <Table.Th scope="col">Details</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {shown.map((e, i) => (
              <Table.Tr key={`${e.at}-${i}`} data-fresh={isFresh(e.at) || undefined}>
                <Table.Td><EventTime at={e.at} /></Table.Td>
                <Table.Td>
                  <Text size="sm" truncate title={`${e.backend} / ${e.model}`}>{e.backend} / {e.model}</Text>
                </Table.Td>
                <Table.Td><StatusBadge status={e.status} /></Table.Td>
                <Table.Td>
                  <Group gap={6} wrap="nowrap" justify="flex-end">
                    {e.message ? (
                      <Tooltip label={e.message} multiline maw={320} events={{ hover: true, focus: true, touch: true }}>
                        <Text size="xs" c="dimmed" truncate tabIndex={0} aria-label={`Error message: ${e.message}`} style={{ cursor: 'default', maxWidth: 88 }}>Message</Text>
                      </Tooltip>
                    ) : (
                      <Text size="xs" c="dimmed">—</Text>
                    )}
                    {e.request_id ? <RequestInspectButton id={e.request_id} /> : <Text size="xs" c="dimmed"> </Text>}
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      </ScrollArea>
      {errors.length > shown.length && (
        <Text size="xs" c="dimmed" mt="xs">Showing the newest {shown.length} of {fmtInt(errors.length)} retained errors.</Text>
      )}
    </Card>
  )
}

export function RecentRequestsCard({
  requests,
  loading,
}: {
  requests: InspectedRequest[]
  loading: boolean
}) {
  const shown = requests.slice(0, 8)
  return (
    <Card p="md" miw={0} className="activity-card">
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mb="sm">
        <Group gap={8} wrap="nowrap" miw={0}>
          <ThemeIcon variant="light" color="indigo" size="sm" radius="md" aria-hidden="true">
            <IconClock size={13} />
          </ThemeIcon>
          <Title order={5} fz={13} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>Recent requests</Title>
        </Group>
        <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
          {requests.length > shown.length
            ? `Newest ${shown.length} of ${fmtInt(requests.length)}`
            : `${fmtInt(requests.length)} attempts`}
        </Text>
      </Group>
      <ScrollArea className="activity-feed" type="auto" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Recent requests' }}>
      {loading ? (
        <Group justify="center" py="sm" role="status"><Loader size="sm" aria-hidden="true" /></Group>
      ) : requests.length === 0 ? (
        <EmptyState
          icon={<IconListDetails size={20} stroke={1.6} />}
          title="No requests captured yet"
          hint="The latest upstream attempts appear here once the proxy serves traffic."
        />
      ) : (
        <Table verticalSpacing="xs" horizontalSpacing="sm" className="recent-requests-table">
          <colgroup><col style={{ width: '23%' }} /><col /><col style={{ width: '14%' }} /><col style={{ width: '20%' }} /><col style={{ width: '20%' }} /></colgroup>
          <Table.Thead>
            <Table.Tr>
              <Table.Th scope="col">Time</Table.Th>
              <Table.Th scope="col">Backend / model</Table.Th>
              <Table.Th scope="col">Wire</Table.Th>
              <Table.Th scope="col">Status</Table.Th>
              <Table.Th aria-hidden="true" />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {shown.map((request) => (
              <Table.Tr key={request.id} data-fresh={isFresh(request.at) || undefined}>
                <Table.Td>
                  <EventTime at={request.at} />
                </Table.Td>
                <Table.Td>
                  <Text size="sm" truncate title={`${request.backend} / ${request.model}`}>{request.backend} / {request.model}</Text>
                </Table.Td>
                <Table.Td><WireLabel kind={request.kind} /></Table.Td>
                <Table.Td><StatusBadge status={request.status} /></Table.Td>
                <Table.Td ta="right"><RequestInspectButton id={request.id} /></Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      </ScrollArea>
    </Card>
  )
}

function RequestInspectButton({ id }: { id: string }) {
  const [opened, setOpened] = useState(false)
  const query = useQuery({ queryKey: ['request', id], queryFn: () => fetchRequest(id), enabled: opened, retry: 1 })
  return (
    <>
      <Button size="compact-xs" variant="subtle" aria-label={`Inspect request ${id}`} onClick={() => setOpened(true)}>Inspect</Button>
      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title={<Title order={5}>Request inspection</Title>}
        size="xl"
        centered
        closeButtonProps={{ 'aria-label': 'Close request inspection' }}
      >
        {query.isPending ? <Group justify="center" py="xl" role="status"><Loader size="sm" aria-hidden="true" /></Group> : query.isError ? (
          query.error instanceof ApiError && query.error.status === 404 ? (
            <Alert color="red">This request is no longer available in the retained history.</Alert>
          ) : (
            <Alert color="red" title="Couldn't load request details">
              <Stack gap="xs" align="flex-start">
                <Text size="sm">Request history is temporarily unavailable. Try again.</Text>
                <Button variant="light" onClick={() => query.refetch({ cancelRefetch: false })} loading={query.isFetching}>Retry</Button>
              </Stack>
            </Alert>
          )
        ) : query.data ? <RequestDetail request={query.data} /> : null}
      </Modal>
    </>
  )
}

function RequestDetail({ request }: { request: InspectedRequest }) {
  return (
    <Stack gap="sm">
      <Group gap="xs" wrap="nowrap">
        <StatusBadge status={request.status} />
        <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{request.backend} / {request.model}</Text>
      </Group>
      <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>Proxy request ID: {request.proxy_request_id || 'unavailable'} · wire format: {request.kind || 'unknown'}</Text>
      {request.error && <Alert color="red" variant="light">{request.error}</Alert>}
      <Alert color="blue" variant="light">
        Request payloads are not retained by the proxy, so prompts, tool inputs, and credentials cannot appear in dashboard history.
      </Alert>
    </Stack>
  )
}

// HTTP state as icon + label so the status is never color-alone. 'error' means
// the upstream never answered — kept distinct from a real 4xx/5xx code.
function StatusBadge({ status }: { status: string }) {
  const severity = statusSeverity(status)
  const color = severityColor(severity)
  const description = statusDescription(status)
  const icon = severity === 'critical' ? <IconServerOff size={11} aria-hidden="true" />
    : severity === 'warning' ? <IconAlertTriangle size={11} aria-hidden="true" />
      : severity === 'good' ? <IconCheck size={11} aria-hidden="true" /> : <IconPointFilled size={11} aria-hidden="true" />
  return (
    <Tooltip label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
      <Badge
        size="sm"
        variant="light"
        color={color}
        aria-label={description}
        leftSection={icon}
        styles={{ root: { fontWeight: 700, cursor: 'default' }, label: { overflow: 'visible' } }}
      >
        {status === 'error' ? 'no response' : status}
      </Badge>
    </Tooltip>
  )
}
