import { useState } from 'react'
import { Alert, Badge, Box, Button, Card, Group, Loader, Modal, Paper, Stack, Table, Text, ThemeIcon, Title, Tooltip } from '@mantine/core'
import { IconAlertTriangle, IconCheck, IconClock, IconListDetails, IconPointFilled, IconServerOff, IconShieldCheck } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchRequest, type InspectedRequest, type UpstreamErrorEvent } from '../../api'
import { fmtInt } from '../../format'
import { EmptyState } from '../EmptyState'
import { statusSeverity, severityColor, statusDescription } from '../../lib/httpStatus'

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

// UpstreamErrorsCard lists the most recent upstream failures newest-first:
// when it happened, which backend/model, the HTTP status (or "no response"),
// and what the upstream said about it.
export function UpstreamErrorsCard({ errors }: { errors: UpstreamErrorEvent[] }) {
  const shown = errors.slice(0, 6)
  return (
    <Card withBorder radius="lg" p="md">
      <div className="overview-card-header">
        <Group gap={8}>
          <ThemeIcon variant="light" size="sm" radius="md" aria-hidden="true"
            styles={{ root: { color: 'var(--data-critical)', background: 'color-mix(in srgb, var(--data-critical) 12%, var(--card))' } }}
          >
            <IconServerOff size={13} />
          </ThemeIcon>
          <Title order={5} className="overview-card-title">Recent upstream errors</Title>
        </Group>
        <Text size="xs" c="dimmed">
          {errors.length === 1 ? '1 failure' : `${fmtInt(errors.length)} failures · newest first`}
        </Text>
      </div>
      {errors.length === 0 ? (
        <EmptyState icon={<IconShieldCheck size={20} />} title="No recent upstream errors"
          hint="No failures are present in this instance's retained error history. This is separate from the chart time range." />
      ) : (
        <Stack gap={6}>
          {shown.map((e, i) => (
            <Paper key={`${e.at}-${i}`} withBorder radius="md" p="xs" bg="var(--sunken)">
              <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
                <Box style={{ minWidth: 0 }}>
                  <div className="overview-error-meta">
                    <StatusBadge status={e.status} />
                    <EventTime at={e.at} />
                  </div>
                  <Text size="sm" fw={500} mt={4} className="overview-error-identity">
                    {e.backend} / {e.model}
                  </Text>
                  {e.message && (
                    <Box component="details" mt={4} className="overview-error-disclosure">
                      <Text component="summary" size="xs" c="dimmed">View error message</Text>
                      <Text size="xs" mt={6} className="overview-error-message">{e.message}</Text>
                    </Box>
                  )}
                  {e.request_id && <RequestInspectButton id={e.request_id} />}
                </Box>
              </Group>
            </Paper>
          ))}
          {errors.length > shown.length && (
            <Text size="xs" c="dimmed">Showing the newest {shown.length} of {fmtInt(errors.length)} retained errors.</Text>
          )}
        </Stack>
      )}
    </Card>
  )
}

export function RecentRequestsCard({
  requests,
  loading,
  isMobile,
}: {
  requests: InspectedRequest[]
  loading: boolean
  isMobile: boolean
}) {
  const shown = requests.slice(0, 8)
  return (
    <Card withBorder radius="lg" p="md">
      <div className="overview-card-header">
        <Group gap={8}>
          <ThemeIcon variant="light" color="brand" size="sm" radius="md" aria-hidden="true">
            <IconClock size={13} />
          </ThemeIcon>
          <Title order={5} className="overview-card-title">Recent requests</Title>
        </Group>
        <Text size="xs" c="dimmed">
          {requests.length > shown.length
            ? `Newest ${shown.length} of ${fmtInt(requests.length)}`
            : `Last ${fmtInt(requests.length)} upstream attempts`}
        </Text>
      </div>
      {loading ? (
        <Group justify="center" py="sm" role="status"><Loader size="sm" aria-hidden="true" /></Group>
      ) : requests.length === 0 ? (
        <EmptyState
          icon={<IconListDetails size={20} stroke={1.6} />}
          title="No requests captured yet"
          hint="The latest upstream attempts appear here once this proxy instance serves traffic."
        />
      ) : isMobile ? (
        <Stack gap={6}>
          {shown.map((request) => (
            <Box key={request.id} className="overview-activity-item">
              <div className="overview-activity-item-main">
                <div className="overview-activity-item-meta">
                  <EventTime at={request.at} />
                  <Text size="xs" c="dimmed" className="overview-identifier">
                    {request.backend} / {request.model}
                  </Text>
                </div>
                <Group gap={6}>
                  <StatusBadge status={request.status} />
                </Group>
              </div>
              <div className="overview-activity-item-actions">
                <RequestInspectButton id={request.id} />
              </div>
            </Box>
          ))}
        </Stack>
      ) : (
        <Table verticalSpacing="xs" horizontalSpacing="sm" className="overview-activity-table">
          <Table.Thead>
            <Table.Tr>
              <Table.Th scope="col">Time</Table.Th>
              <Table.Th scope="col">Backend / model</Table.Th>
              <Table.Th scope="col">Status</Table.Th>
              <Table.Th aria-hidden="true" />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {shown.map((request) => (
              <Table.Tr key={request.id}>
                <Table.Td>
                  <EventTime at={request.at} />
                </Table.Td>
                <Table.Td>
                  <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{request.backend} / {request.model}</Text>
                </Table.Td>
                <Table.Td><StatusBadge status={request.status} /></Table.Td>
                <Table.Td ta="right"><RequestInspectButton id={request.id} /></Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
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
          <Alert color="red">This request is no longer available. The in-memory history keeps the latest 50 attempts per instance.</Alert>
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
