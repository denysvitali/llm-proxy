import { useEffect, useState } from 'react'
import { Alert, Button, Card, Group, Loader, Modal, ScrollArea, Stack, Table, Text, Tooltip, Title } from '@mantine/core'
import { IconClock, IconListDetails, IconServerOff, IconShieldCheck } from '@tabler/icons-react'
import { motion } from 'framer-motion'
import { useQuery } from '@tanstack/react-query'
import { ApiError, fetchRequest, type InspectedRequest, type UpstreamErrorEvent } from '../../api'
import { fmtInt } from '../../format'
import { EmptyState } from '../EmptyState'
import { statusSeverity, severityColor, statusDescription } from '../../lib/httpStatus'
import { springSnappy, useMotionPreset } from '../../motion/tokens'

// A captured attempt is treated as freshly landed (and animated in) when the
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
    return <Text size="xs" c="dimmed" className="tabular">{at}</Text>
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
        className="tabular"
        style={{ cursor: 'default', borderRadius: 4 }}
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

// Compact severity mark: a status dot plus the code as text, so severity is
// never conveyed by color alone. `error` means the upstream never answered.
const severityToken: Record<string, string> = {
  critical: 'var(--data-critical)',
  warning: 'var(--data-warning)',
  good: 'var(--data-good)',
  neutral: 'var(--muted)',
}

function StatusMark({ status }: { status: string }) {
  const severity = statusSeverity(status)
  const color = severityColor(severity)
  const description = statusDescription(status)
  return (
    <Tooltip label={description} events={{ hover: true, focus: true, touch: true }}>
      <span className="overview-status" role="img" aria-label={description} tabIndex={0} data-status={status}>
        <span className="overview-status-dot" aria-hidden="true" style={{ background: severityToken[severity] ?? 'var(--muted)' }} />
        <Text size="xs" fw={650} c={severity === 'good' ? 'dimmed' : color} className="tabular overview-status-code">
          {status === 'error' ? 'no response' : status}
        </Text>
      </span>
    </Tooltip>
  )
}

function CardHead({ icon, title, detail }: { icon: React.ReactNode; title: string; detail: string }) {
  return (
    <div className="overview-card-head">
      <Group gap={8} wrap="nowrap" miw={0}>
        <span className="overview-feed-icon" aria-hidden="true">{icon}</span>
        <Title order={5} fz={13} className="overview-feed-title">{title}</Title>
      </Group>
      <Text size="xs" c="dimmed" className="overview-feed-count">{detail}</Text>
    </div>
  )
}

// Live rows settle in with a spring rather than a CSS flash. Only rows that
// arrive AFTER the feed has painted animate: the first batch (page load,
// refresh with retained history) mounts at rest, so screenshots and refreshes
// never replay the animation, and there is no mid-flight layout jitter.
// Reduced motion renders everything at its final position and disables layout
// moves entirely.
function MotionRow({
  fresh,
  enter,
  children,
  className,
}: {
  fresh: boolean
  enter: boolean
  children: React.ReactNode
  className?: string
}) {
  const { reduced } = useMotionPreset()
  if (reduced) return <Table.Tr data-fresh={fresh || undefined} className={className}>{children}</Table.Tr>
  return (
    // No layout animation on table rows: framer's layout projection on <tr>
    // briefly offsets cells, which reads as the table (and everything below
    // it) shifting when a live request lands. A fade-only entrance keeps the
    // geometry stable while the row still announces itself.
    <motion.tr
      initial={enter ? { opacity: 0 } : false}
      animate={{ opacity: 1 }}
      transition={springSnappy}
      className={className}
      data-fresh={fresh || undefined}
    >
      {children}
    </motion.tr>
  )
}

// Ids that were not present on the previous render. The previous ids are held
// in component state (not a ref), so the comparison is a pure render-time
// read and stays correct under React's concurrent double-render.
function useEnteringIds(ids: string[]): Set<string> {
  const [previous, setPrevious] = useState<ReadonlySet<string> | null>(null)
  const signature = ids.join('\u0000')
  useEffect(() => {
    setPrevious(new Set(ids))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature])
  const entering = new Set<string>()
  if (previous) for (const id of ids) if (!previous.has(id)) entering.add(id)
  return entering
}

// UpstreamErrorsCard lists the most recent upstream failures newest-first:
// when it happened, which backend/model, the HTTP status (or "no response"),
// and what the upstream said about it.
export function UpstreamErrorsCard({ errors }: { errors: UpstreamErrorEvent[] }) {
  const entering = useEnteringIds(errors.map((e, i) => `${e.at}-${i}`))
  return (
    <Card p="md" miw={0} className="activity-card">
      <CardHead
        icon={<IconServerOff size={13} />}
        title="Recent upstream errors"
        detail={errors.length === 0 ? 'None retained' : errors.length === 1 ? '1 failure · newest first' : `${fmtInt(errors.length)} failures · newest first`}
      />
      <ScrollArea className="activity-feed" type="auto" scrollbars="y" offsetScrollbars="present" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Recent upstream errors' }}>
        {errors.length === 0 ? (
          <EmptyState icon={<IconShieldCheck size={20} />} title="No recent upstream errors"
            hint="No failures are present in the retained error history. This is separate from the chart time range." />
        ) : (
          <Table className="upstream-errors-table" verticalSpacing={6} horizontalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th scope="col">Time</Table.Th>
                <Table.Th scope="col">Backend / model</Table.Th>
                <Table.Th scope="col">Status</Table.Th>
                <Table.Th scope="col" ta="right">Details</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {errors.map((e, i) => (
                <MotionRow key={`${e.at}-${i}`} enter={entering.has(`${e.at}-${i}`)} fresh={isFresh(e.at)}>
                  <Table.Td><EventTime at={e.at} /></Table.Td>
                  <Table.Td>
                    <Text size="xs" className="overview-ident" title={`${e.backend} / ${e.model}`}>{e.backend} / {e.model}</Text>
                  </Table.Td>
                  <Table.Td><StatusMark status={e.status} /></Table.Td>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap" justify="flex-end" className="overview-error-details">
                      {e.message ? (
                        <Tooltip label={e.message} multiline maw={320} events={{ hover: true, focus: true, touch: true }}>
                          <Text size="xs" c="dimmed" truncate tabIndex={0} aria-label={`Error message: ${e.message}`} style={{ cursor: 'default', minWidth: 0, maxWidth: '100%' }}>Message</Text>
                        </Tooltip>
                      ) : (
                        <Text size="xs" c="dimmed">—</Text>
                      )}
                      {e.request_id ? <RequestInspectButton id={e.request_id} /> : null}
                    </Group>
                  </Table.Td>
                </MotionRow>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </ScrollArea>
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
  const entering = useEnteringIds(requests.map((request) => request.id))
  return (
    <Card p="md" miw={0} className="activity-card">
      <CardHead
        icon={<IconClock size={13} />}
        title="Recent requests"
        detail={requests.length === 0 ? 'None captured' : `${fmtInt(requests.length)} attempts · newest first`}
      />
      <ScrollArea className="activity-feed" type="auto" scrollbars="y" offsetScrollbars="present" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Recent requests' }}>
        {loading ? (
          <Group justify="center" py="sm" role="status"><Loader size="sm" aria-hidden="true" /></Group>
        ) : requests.length === 0 ? (
          <EmptyState
            icon={<IconListDetails size={20} stroke={1.6} />}
            title="No requests captured yet"
            hint="The latest upstream attempts appear here once the proxy serves traffic."
          />
        ) : (
          <Table verticalSpacing={6} horizontalSpacing="sm" className="recent-requests-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th scope="col">Time</Table.Th>
                <Table.Th scope="col">Backend / model</Table.Th>
                <Table.Th scope="col">Wire</Table.Th>
                <Table.Th scope="col">Status</Table.Th>
                <Table.Th scope="col" ta="right">Inspect</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {requests.map((request) => (
                <MotionRow key={request.id} enter={entering.has(request.id)} fresh={isFresh(request.at)}>
                  <Table.Td><EventTime at={request.at} /></Table.Td>
                  <Table.Td>
                    <Text size="xs" className="overview-ident" title={`${request.backend} / ${request.model}`}>{request.backend} / {request.model}</Text>
                  </Table.Td>
                  <Table.Td><WireLabel kind={request.kind} /></Table.Td>
                  <Table.Td><StatusMark status={request.status} /></Table.Td>
                  <Table.Td ta="right"><RequestInspectButton id={request.id} /></Table.Td>
                </MotionRow>
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
        <StatusMark status={request.status} />
        <Text size="sm" className="overview-ident">{request.backend} / {request.model}</Text>
      </Group>
      <Text size="xs" c="dimmed" className="overview-ident">Proxy request ID: {request.proxy_request_id || 'unavailable'} · wire format: {request.kind || 'unknown'}</Text>
      {request.error && <Alert color="red" variant="light">{request.error}</Alert>}
      <Alert color="blue" variant="light">
        Request payloads are not retained by the proxy, so prompts, tool inputs, and credentials cannot appear in dashboard history.
      </Alert>
    </Stack>
  )
}
