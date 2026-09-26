import { useState, useMemo } from 'react'
import {
  Box,
  Accordion,
  Alert,
  Badge,
  Button,
  Card,
  Code,
  Group,
  Loader,
  Modal,
  Paper,
  ScrollArea,
  SimpleGrid,
  Stack,
  Table,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
} from '@mantine/core'
import {
  IconActivity,
  IconAlertTriangle,
  IconBolt,
  IconCheck,
  IconPointFilled,
  IconCoins,
  IconShieldCheck,
  IconTool,
  IconServerOff,
  IconInboxOff,
  IconChartBar,
  IconClock,
  IconListDetails,
} from '@tabler/icons-react'
import { BarChart } from '@mantine/charts'
import { useMediaQuery } from '@mantine/hooks'
import { useQuery } from '@tanstack/react-query'
import { fetchGrokUsage, fetchOverview, fetchRequest, fetchRequests, fetchStats, fetchStatsSeries, fetchUpstreamErrors, fetchZcodeUsage } from '../api'
import { useLiveStatsUpdates } from '../useLiveUpdates'
import type { InspectedRequest, SeriesPoint, UpstreamErrorEvent } from '../api'
import GrokUsageCard from '../components/GrokUsageCard'
import ZcodeUsageCard from '../components/ZcodeUsageCard'
import { PageHeader } from '../components/PageHeader'
import { PageSection } from '../components/PageSection'
import { TimeRangeControl } from '../components/TimeRangeControl'
import { clampRate, fmtInt, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import StatTile from '../components/StatTile'
import StatusChips from '../components/StatusChips'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend } from '../components/TokenMixBar'
import { HistoryLineChart, historyData, historyFormatters } from '../components/HistoryCharts'
import { EmptyState } from '../components/EmptyState'
import Fade from '../components/Fade'
import ErrorRetryCard from '../components/ErrorRetryCard'
import { providerSegments, providerAggregates } from '../lib/stats'
import { statusSeverity, severityColor, statusDescription } from '../lib/httpStatus'

export default function OverviewPage() {
  useLiveStatsUpdates()
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const grokUsageEnabled = ovQ.data?.grokUsage.configured ?? false
  const grokUsageQ = useQuery({
    queryKey: ['grok-usage'],
    queryFn: fetchGrokUsage,
    enabled: grokUsageEnabled,
    refetchInterval: 60_000,
    retry: 1,
  })
  const zcodeUsageEnabled = ovQ.data?.zcodeUsage.configured ?? false
  const zcodeUsageQ = useQuery({
    queryKey: ['zcode-usage'],
    queryFn: fetchZcodeUsage,
    enabled: zcodeUsageEnabled,
    refetchInterval: 60_000,
    retry: 1,
  })
  const [range, setRange] = useState('24h')
  const seriesQ = useQuery({
    queryKey: ['stats-series', range],
    queryFn: () => fetchStatsSeries(range),
  })
  const errorsQ = useQuery({
    queryKey: ['upstream-errors'],
    queryFn: fetchUpstreamErrors,
    refetchInterval: 30_000,
    retry: 1,
  })
  const requestsQ = useQuery({ queryKey: ['recent-requests'], queryFn: fetchRequests, refetchInterval: 30_000 })

  const models = statsQ.data?.models ?? []
  const statsError = statsQ.error instanceof Error ? statsQ.error.message : undefined
  const pal = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  const derived = useMemo(() => {
    const totalRequests = models.reduce((s, m) => s + m.requests, 0)
    const totalSuccess = models.reduce((s, m) => s + m.successes, 0)
    const tokensIn = models.reduce((s, m) => s + m.input_tokens, 0)
    const tokensOut = models.reduce((s, m) => s + m.output_tokens, 0)
    const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
    const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)
    const busy = models.filter((m) => m.throughput_tps.p50 > 0)
    let medianTps = 0
    if (busy.length > 0) {
      const sorted = [...busy].sort(
        (a, b) => a.throughput_tps.p50 - b.throughput_tps.p50,
      )
      medianTps = sorted[Math.floor(sorted.length / 2)].throughput_tps.p50
    }
    return { totalRequests, totalSuccess, tokensIn, tokensOut, toolErrors, toolCalls, medianTps }
    // models is a new ?? [] array every render; memo still avoids recomputes on other state changes
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [models])

  const topModels = useMemo(() =>
    [...models]
      .sort((a, b) => b.requests - a.requests)
      .slice(0, 8)
      .map((m) => ({
        model: `${m.backend}/${m.model}`,
        requests: m.requests,
      })), [models, isMobile])

  const history = useMemo(() => ({
    latency: historyData([seriesQ.data?.series.ttft_p50, seriesQ.data?.series.e2e_p50]),
    throughput: historyData([seriesQ.data?.series.throughput_p50]),
    tokens: historyData([seriesQ.data?.series.tokens_in, seriesQ.data?.series.tokens_out]),
  }), [seriesQ.data])

  const requestCount = sumPoints(seriesQ.data?.series.requests)
  const mix = providerSegments(models, pal.series)
  const bothUsage = grokUsageEnabled && zcodeUsageEnabled
  const ov = ovQ.data

  return (
    <Fade pending={statsQ.isPending || ovQ.isPending}>
      <Stack gap="lg">
        <PageHeader
          title="Overview"
          subtitle="Your gateway at a glance. Follow traffic, performance, and reliability."
          extra={ov ? <Badge variant="default" tt="none" size="lg" visibleFrom="sm">Providers: {ov.backends.length}</Badge> : undefined}
        />

        {ovQ.isError && (
          <ErrorRetryCard
            title="Instance details unavailable"
            message="Configuration and subscription visibility could not be refreshed. Traffic statistics are loaded separately."
            onRetry={() => ovQ.refetch({ cancelRefetch: false })}
            retrying={ovQ.isFetching}
          />
        )}

        <PageSection title="Traffic summary" description="All recorded statistics · independent of the chart time range">
          {statsQ.data ? (
            <SimpleGrid cols={{ base: 2, sm: 3, lg: 6 }} spacing="md">
              <StatTile
                label="Requests"
                value={fmtInt(derived.totalRequests)}
                hint={`${fmtInt(derived.totalSuccess)} succeeded`}
                icon={<IconActivity size={16} />}
              />
              <StatTile
                label="Success rate"
                value={derived.totalRequests ? `${(100 * derived.totalSuccess / derived.totalRequests).toFixed(1)}%` : '—'}
                hint="succeeded / total"
                icon={<IconShieldCheck size={16} />}
              />
              <StatTile
                label="Tokens served"
                value={fmtInt(derived.tokensIn + derived.tokensOut)}
                hint={`${fmtInt(derived.tokensIn)} in · ${fmtInt(derived.tokensOut)} out`}
                icon={<IconCoins size={16} />}
              />
              <StatTile
                label="Median tok/s"
                value={fmtTps(derived.medianTps)}
                hint="p50 across models"
                icon={<IconBolt size={16} />}
              />
              <StatTile
                label="Tool calls"
                value={fmtInt(derived.toolCalls)}
                hint={`${fmtInt(derived.toolErrors)} errored`}
                icon={<IconTool size={16} />}
              />
              <StatTile
                label="Tool error rate"
                value={derived.toolCalls ? `${(100 * clampRate(derived.toolErrors / derived.toolCalls)).toFixed(1)}%` : '—'}
                hint={`${fmtInt(derived.toolErrors)} errored · ${fmtInt(derived.toolCalls)} calls`}
                icon={<IconAlertTriangle size={16} />}
                accent={derived.toolErrors > 0 ? 'red' : undefined}
              />
            </SimpleGrid>
          ) : statsQ.isPending ? (
            <Group justify="center" py="xl" role="status"><Loader size="sm" /><Text size="sm" c="dimmed">Loading traffic statistics…</Text></Group>
          ) : null}

          {statsQ.isError ? (
            <ErrorRetryCard
              title="Couldn't load proxy statistics"
              message={
                statsError
                  ? `Model statistics are temporarily unavailable. ${statsError}`
                  : 'Model statistics are temporarily unavailable.'
              }
              onRetry={() => statsQ.refetch({ cancelRefetch: false })}
              retrying={statsQ.isFetching}
            />
          ) : models.length === 0 && !statsQ.isPending ? (
            <EmptyState
              icon={<IconInboxOff size={20} stroke={1.6} />}
              title="No model traffic yet"
              hint="Send a request through the proxy and per-model stats will land here."
            />
          ) : null}
        </PageSection>

        <PageSection
          title="Performance over time"
          description={seriesQ.isError
            ? 'History unavailable'
            : `${requestCount.toLocaleString('en-US')} requests in range`}
          extra={<TimeRangeControl value={range} onChange={setRange} />}
        >
          <Card withBorder radius="lg" p="md">
            {seriesQ.isPending ? (
              <Group justify="center" py="xl"><Loader size="sm" /></Group>
            ) : seriesQ.isError ? (
              <ErrorRetryCard
                title="Couldn't load time history"
                message={<>History is unavailable. Check connectivity and whether <Code>stats.persist_file</Code> or Redis history is configured.</>}
                onRetry={() => seriesQ.refetch({ cancelRefetch: false })}
                retrying={seriesQ.isFetching}
              />
            ) : (
              <SimpleGrid cols={{ base: 1, xl: 3 }} spacing="lg">
                <HistoryLineChart
                  title="Latency"
                  description="Median first byte and full response"
                  data={history.latency}
                  series={[
                    { name: 'series0', label: 'First byte', formatter: historyFormatters.seconds },
                    { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds },
                  ]}
                  height={200}
                />
                <HistoryLineChart
                  title="Throughput"
                  description="Median output rate"
                  data={history.throughput}
                  series={[{ name: 'series0', label: 'Tokens/sec', formatter: historyFormatters.tps }]}
                  height={200}
                />
                <HistoryLineChart
                  title="Token volume"
                  description="Input and output tokens"
                  data={history.tokens}
                  series={[
                    { name: 'series0', label: 'Input', formatter: historyFormatters.count },
                    { name: 'series1', label: 'Output', formatter: historyFormatters.count },
                  ]}
                  height={200}
                />
              </SimpleGrid>
            )}
          </Card>
        </PageSection>

        {(grokUsageEnabled || zcodeUsageEnabled) && (
          <Accordion variant="separated" radius="lg">
            <Accordion.Item value="subscriptions">
              <Accordion.Control>Subscription usage <Text component="span" size="xs" c="dimmed" ml="sm">{[grokUsageEnabled && 'Grok', zcodeUsageEnabled && 'ZCode'].filter(Boolean).join(' / ')} account quotas</Text></Accordion.Control>
              <Accordion.Panel>
                {bothUsage ? (
                  <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
                    <GrokUsageCard query={grokUsageQ} />
                    <ZcodeUsageCard query={zcodeUsageQ} />
                  </SimpleGrid>
                ) : grokUsageEnabled ? (
                  <GrokUsageCard query={grokUsageQ} />
                ) : (
                  <ZcodeUsageCard query={zcodeUsageQ} />
                )}
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        )}

        <PageSection title="Traffic breakdown">
          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="lg">
            <Card withBorder radius="lg" p="md">
              <Group justify="space-between" align="center" wrap="nowrap" gap="xs" mb="sm">
                <Title order={5}>Requests by model</Title>
                {topModels.length > 0 && (
                  <Badge size="sm" variant="light" color="gray" styles={{ root: { cursor: 'default' } }}>
                    top {topModels.length}
                  </Badge>
                )}
              </Group>
              {topModels.length === 0 ? (
                <EmptyState
                  icon={<IconChartBar size={20} stroke={1.6} />}
                  title="No model traffic yet"
                  hint="Requests are ranked per model once traffic flows through the proxy."
                />
              ) : (
                <Stack gap="sm">
                  <BarChart
                    h={300}
                    data={topModels}
                    dataKey="model"
                    orientation="vertical"
                    series={[{ name: 'requests', color: pal.magnitude }]}
                    withBarValueLabel
                    valueFormatter={fmtInt}
                    gridAxis="none"
                    withXAxis={false}
                    barProps={{ radius: [0, 4, 4, 0], barSize: 18 }}
                    yAxisProps={{
                      width: isMobile ? 122 : 176,
                      tickLine: false,
                    }}
                    tooltipAnimationDuration={150}
                  />
                  <Accordion variant="contained" radius="md">
                    <Accordion.Item value="model-counts">
                      <Accordion.Control>View model request data</Accordion.Control>
                      <Accordion.Panel>
                        <Table layout="fixed">
                          <Table.Caption>Top models by request count, including provider identity</Table.Caption>
                          <Table.Thead><Table.Tr>
                            <Table.Th scope="col">Provider / model</Table.Th>
                            <Table.Th scope="col" ta="right" w={90}>Requests</Table.Th>
                          </Table.Tr></Table.Thead>
                          <Table.Tbody>{topModels.map((m) => (
                            <Table.Tr key={m.model}>
                              <Table.Td style={{ overflowWrap: 'anywhere' }}>{m.model}</Table.Td>
                              <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>{m.requests.toLocaleString('en-US')}</Table.Td>
                            </Table.Tr>
                          ))}</Table.Tbody>
                        </Table>
                      </Accordion.Panel>
                    </Accordion.Item>
                  </Accordion>
                </Stack>
              )}
            </Card>

            <Card withBorder radius="lg" p="md">
              <Title order={5} mb="sm">Token mix by provider</Title>
              {mix.length === 0 ? (
                <EmptyState
                  icon={<IconCoins size={20} stroke={1.6} />}
                  title="No provider traffic yet"
                  hint="Token composition appears here once providers serve requests."
                />
              ) : (
                <Stack gap="md">
                  {mix.map(([backend, segs]) => {
                    const total = segs.reduce((s, seg) => s + seg.value, 0)
                    return (
                      <Box key={backend}>
                        <Group justify="space-between" mb={4} align="baseline" gap="xs">
                          <Text size="sm" fw={600}>{backend}</Text>
                          {total > 0 && (
                            <Text size="xs" c="dimmed" style={{ fontVariantNumeric: 'tabular-nums' }}>
                              {fmtInt(total)} tokens
                            </Text>
                          )}
                        </Group>
                        <TokenMixBar segments={segs} />
                        <TokenLegend segments={segs} showPercent />
                      </Box>
                    )
                  })}
                </Stack>
              )}
            </Card>
          </SimpleGrid>
        </PageSection>

        <PageSection title="Provider uptime" description="Request success per backend over recorded traffic">
          <Card withBorder radius="lg" p="md">
            {models.length === 0 && !statsQ.isPending ? (
              <EmptyState
                icon={<IconShieldCheck size={20} stroke={1.6} />}
                title="No providers with traffic yet"
                hint="Backend health appears here after the first request is routed."
              />
            ) : (
              <Table.ScrollContainer minWidth={640}>
                <Table verticalSpacing="xs" horizontalSpacing="sm">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th scope="col">Backend</Table.Th>
                      <Table.Th scope="col">Status</Table.Th>
                      <Table.Th scope="col" ta="right">Uptime</Table.Th>
                      {!isMobile && <Table.Th scope="col">Upstream errors</Table.Th>}
                      {!isMobile && <Table.Th scope="col" ta="right">Requests</Table.Th>}
                      {!isMobile && <Table.Th scope="col" ta="right">Tool err</Table.Th>}
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {providerAggregates(models).map((p) => (
                      <Table.Tr key={p.backend}>
                        <Table.Td>
                          <Text size="sm" fw={600} style={{ overflowWrap: 'anywhere' }}>{p.backend}</Text>
                        </Table.Td>
                        <Table.Td>
                          <UptimeBadge uptime={p.uptime} requests={p.requests} />
                        </Table.Td>
                        <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                          {p.requests > 0 ? historyFormatters.percent(p.uptime) : '—'}
                        </Table.Td>
                        {!isMobile && (
                          <Table.Td>
                            {Object.keys(p.statusCodes).length > 0 ? (
                              <StatusChips codes={p.statusCodes} />
                            ) : (
                              <Text size="xs" c="dimmed">none</Text>
                            )}
                          </Table.Td>
                        )}
                        {!isMobile && (
                          <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                            {fmtInt(p.requests)}
                          </Table.Td>
                        )}
                        {!isMobile && (
                          <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                            {p.toolCalls > 0 ? historyFormatters.percent(clampRate(p.toolErrors / p.toolCalls)) : '—'}
                          </Table.Td>
                        )}
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Card>
        </PageSection>

        <PageSection
          title="Recent activity"
          description="Instance-local upstream attempts · refreshed every 30 seconds · not filtered by chart range"
        >
          <Stack gap="sm">
            {requestsQ.isError && (
              <ErrorRetryCard title="Couldn't refresh recent requests"
                message={requestsQ.data ? 'Showing the last available attempts; this list may be out of date.' : 'Request history could not be loaded. Retry to check recent attempts.'}
                onRetry={() => requestsQ.refetch({ cancelRefetch: false })} retrying={requestsQ.isFetching} />
            )}
            {(requestsQ.data || requestsQ.isPending) && (
              <RecentRequestsCard requests={requestsQ.data?.requests ?? []} loading={requestsQ.isPending} isMobile={isMobile} />
            )}
            {errorsQ.isError && (
              <ErrorRetryCard title="Couldn't refresh upstream errors"
                message={errorsQ.data ? 'Showing the last available errors; this list may be out of date.' : 'The error feed is unavailable. This does not mean there were no failures.'}
                onRetry={() => errorsQ.refetch({ cancelRefetch: false })} retrying={errorsQ.isFetching} />
            )}
            {errorsQ.data ? <UpstreamErrorsCard errors={errorsQ.data.errors ?? []} /> : errorsQ.isPending ? (
              <Group justify="center" py="md" role="status"><Loader size="sm" /><Text size="sm" c="dimmed">Loading upstream errors…</Text></Group>
            ) : null}
          </Stack>
        </PageSection>
      </Stack>
    </Fade>
  )
}

// Compact timestamp: same-day events show clock time, older ones get a short
// date + time; the tooltip always carries the full locale timestamp so the
// exact moment is never hidden by the compact form.
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

function sumPoints(points?: SeriesPoint[]) {
  return Math.round((points ?? []).reduce((sum, point) => sum + point.value, 0))
}

// UpstreamErrorsCard lists the most recent upstream failures newest-first:
// when it happened, which backend/model, the HTTP status (or "no response"),
// and what the upstream said about it.
function UpstreamErrorsCard({ errors }: { errors: UpstreamErrorEvent[] }) {
  const shown = errors.slice(0, 6)
  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={10}>
        <Group gap={8}>
          <ThemeIcon variant="light" color="red" size="sm" radius="md" aria-hidden="true">
            <IconServerOff size={13} />
          </ThemeIcon>
          <Title order={5}>Recent upstream errors</Title>
        </Group>
        <Text size="xs" c="dimmed">
          {errors.length === 1 ? '1 failure' : `${fmtInt(errors.length)} failures · newest first`}
        </Text>
      </Group>
      {errors.length === 0 ? (
        <EmptyState icon={<IconShieldCheck size={20} />} title="No recent upstream errors"
          hint="No failures are present in this instance's retained error history. This is separate from the chart time range." />
      ) : <Stack gap={6}>
        {shown.map((e, i) => (
          <Paper key={`${e.at}-${i}`} withBorder radius="md" p="xs" bg="var(--sunken)">
            <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
              <Box style={{ minWidth: 0 }}>
                <Group gap={6} wrap="nowrap">
                  <StatusBadge status={e.status} />
                  <EventTime at={e.at} />
                </Group>
                <Text size="sm" fw={500} mt={4} style={{ overflowWrap: 'anywhere' }}>
                  {e.backend} / {e.model}
                </Text>
                {e.message && (
                  <Box component="details" mt={4} style={{ overflowWrap: 'anywhere' }}>
                    <Text component="summary" size="xs" c="dimmed" style={{ cursor: 'pointer' }}>View error message</Text>
                    <Text size="xs" mt={6} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{e.message}</Text>
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
      </Stack>}
    </Card>
  )
}

function RecentRequestsCard({
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
      <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={10}>
        <Group gap={8}>
          <ThemeIcon variant="light" color="brand" size="sm" radius="md" aria-hidden="true">
            <IconClock size={13} />
          </ThemeIcon>
          <Title order={5}>Recent requests</Title>
        </Group>
        <Text size="xs" c="dimmed">
          {requests.length > shown.length
            ? `Newest ${shown.length} of ${fmtInt(requests.length)}`
            : `Last ${fmtInt(requests.length)} upstream attempts`}
        </Text>
      </Group>
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
            <Paper key={request.id} withBorder radius="md" p="xs">
              <Group justify="space-between" align="center" wrap="nowrap" gap="xs">
                <Box style={{ minWidth: 0 }}>
                  <Group gap={6} wrap="nowrap">
                    <EventTime at={request.at} />
                    <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
                      {request.backend} / {request.model}
                    </Text>
                  </Group>
                  <Group gap={6} mt={4}>
                    <StatusBadge status={request.status} />
                  </Group>
                </Box>
                <RequestInspectButton id={request.id} />
              </Group>
            </Paper>
          ))}
        </Stack>
      ) : (
        <ScrollArea>
          <Table verticalSpacing="xs" horizontalSpacing="sm">
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
        </ScrollArea>
      )}
    </Card>
  )
}

function RequestInspectButton({ id }: { id: string }) {
  const [opened, setOpened] = useState(false)
  const query = useQuery({ queryKey: ['request', id], queryFn: () => fetchRequest(id), enabled: opened, retry: 1 })
  return <>
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
}

function RequestDetail({ request }: { request: InspectedRequest }) {
  return <Stack gap="sm">
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
