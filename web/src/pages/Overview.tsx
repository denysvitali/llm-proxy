import { useState, type ReactNode } from 'react'
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
  IconRefresh,
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
import type { InspectedRequest, ModelStat, SeriesPoint, UpstreamErrorEvent } from '../api'
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
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { HistoryLineChart, historyData, historyFormatters } from '../components/HistoryCharts'
import { EmptyState } from '../components/EmptyState'
import { Fade } from '../App'

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

  const totalRequests = models.reduce((s, m) => s + m.requests, 0)
  const totalSuccess = models.reduce((s, m) => s + m.successes, 0)
  const tokensIn = models.reduce((s, m) => s + m.input_tokens, 0)
  const tokensOut = models.reduce((s, m) => s + m.output_tokens, 0)
  const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
  const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)

  // Median of active per-model p50s, not a request-weighted fleet percentile.
  const busy = models.filter((m) => m.throughput_tps.p50 > 0)
  let medianTps = 0
  if (busy.length > 0) {
    const sorted = [...busy].sort(
      (a, b) => a.throughput_tps.p50 - b.throughput_tps.p50,
    )
    medianTps = sorted[Math.floor(sorted.length / 2)].throughput_tps.p50
  }

  const labelMax = isMobile ? 15 : 22
  const topModels = [...models]
    .sort((a, b) => b.requests - a.requests)
    .slice(0, 8)
    .map((m) => ({
      model: `${m.backend}/${m.model}`,
      requests: m.requests,
    }))

  const requestCount = sumPoints(seriesQ.data?.series.requests)
  const mix = providerSegments(models, pal.series)
  const bothUsage = grokUsageEnabled && zcodeUsageEnabled
  const ov = ovQ.data

  return (
    <Fade pending={statsQ.isPending || ovQ.isPending}>
      <Stack gap="lg">
        <PageHeader
          title="Overview"
          subtitle={ov ? `${ov.name} v${ov.version} · ${ov.listen} · auth ${ov.authEnabled ? 'on' : 'off'}` : undefined}
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
                value={fmtInt(totalRequests)}
                hint={`${fmtInt(totalSuccess)} succeeded`}
                icon={<IconActivity size={16} />}
                accent="brand"
              />
              <StatTile
                label="Success rate"
                value={totalRequests ? `${(100 * totalSuccess / totalRequests).toFixed(1)}%` : '—'}
                hint="succeeded / total"
                icon={<IconShieldCheck size={16} />}
                accent="teal"
              />
              <StatTile
                label="Tokens served"
                value={fmtInt(tokensIn + tokensOut)}
                hint={`${fmtInt(tokensIn)} in · ${fmtInt(tokensOut)} out`}
                icon={<IconCoins size={16} />}
                accent="grape"
              />
              <StatTile
                label="Median tok/s"
                value={fmtTps(medianTps)}
                hint="p50 across models"
                icon={<IconBolt size={16} />}
                accent="orange"
              />
              <StatTile
                label="Tool calls"
                value={fmtInt(toolCalls)}
                hint={`${fmtInt(toolErrors)} errored`}
                icon={<IconTool size={16} />}
                accent="brand"
              />
              <StatTile
                label="Tool error rate"
                value={toolCalls ? `${(100 * clampRate(toolErrors / toolCalls)).toFixed(1)}%` : '—'}
                hint={`${fmtInt(toolErrors)} errored · ${fmtInt(toolCalls)} calls`}
                icon={<IconAlertTriangle size={16} />}
                accent={toolErrors > 0 ? 'red' : 'gray'}
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

        {(grokUsageEnabled || zcodeUsageEnabled) && (
          bothUsage ? (
            <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
              <GrokUsageCard query={grokUsageQ} />
              <ZcodeUsageCard query={zcodeUsageQ} />
            </SimpleGrid>
          ) : grokUsageEnabled ? (
            <GrokUsageCard query={grokUsageQ} />
          ) : (
            <ZcodeUsageCard query={zcodeUsageQ} />
          )
        )}

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
              <SimpleGrid cols={{ base: 1, lg: 3 }} spacing="lg">
                <HistoryLineChart
                  title="Latency"
                  description="Median first byte and full response"
                  data={historyData([seriesQ.data?.series.ttft_p50, seriesQ.data?.series.e2e_p50])}
                  series={[
                    { name: 'series0', label: 'First byte', formatter: historyFormatters.seconds },
                    { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds },
                  ]}
                  height={180}
                />
                <HistoryLineChart
                  title="Throughput"
                  description="Median output rate"
                  data={historyData([seriesQ.data?.series.throughput_p50])}
                  series={[{ name: 'series0', label: 'Tokens/sec', formatter: historyFormatters.tps }]}
                  height={180}
                />
                <HistoryLineChart
                  title="Token volume"
                  description="Input and output tokens"
                  data={historyData([seriesQ.data?.series.tokens_in, seriesQ.data?.series.tokens_out])}
                  series={[
                    { name: 'series0', label: 'Input', formatter: historyFormatters.count },
                    { name: 'series1', label: 'Output', formatter: historyFormatters.count },
                  ]}
                  height={180}
                />
              </SimpleGrid>
            )}
          </Card>
        </PageSection>

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
                    h={Math.max(topModels.length * 40 + 16, 120)}
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
                      tickFormatter: (value: string) => value.length > labelMax ? `${value.slice(0, labelMax - 1)}…` : value,
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
              <ScrollArea>
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
              </ScrollArea>
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
            {errorsQ.data ? <UpstreamErrorsCard errors={errorsQ.data.errors} /> : errorsQ.isPending ? (
              <Group justify="center" py="md" role="status"><Loader size="sm" /><Text size="sm" c="dimmed">Loading upstream errors…</Text></Group>
            ) : null}
          </Stack>
        </PageSection>
      </Stack>
    </Fade>
  )
}

function ErrorRetryCard({ title, message, onRetry, retrying }: {
  title: string
  message: ReactNode
  onRetry: () => unknown
  retrying: boolean
}) {
  return (
    <Alert color="red" variant="light" title={title} icon={<IconAlertTriangle size={16} />}>
      <Stack gap="sm" align="flex-start">
        <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{message}</Text>
        <Button size="xs" variant="light" color="red" loading={retrying} disabled={retrying}
          leftSection={<IconRefresh size={14} />} onClick={() => { if (!retrying) void onRetry() }}>
          Retry loading
        </Button>
      </Stack>
    </Alert>
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

export function providerSegments(
  models: ModelStat[],
  colors: string[],
): [string, MixSegment[]][] {
  const kinds = [
    (m: ModelStat) => m.input_tokens,
    (m: ModelStat) => m.output_tokens,
    (m: ModelStat) => m.cache_read_tokens,
    (m: ModelStat) => m.cache_write_tokens,
  ]
  const names = ['input', 'output', 'cache read', 'cache write']
  const byBackend = new Map<string, ModelStat[]>()
  for (const m of models) {
    byBackend.set(m.backend, [...(byBackend.get(m.backend) ?? []), m])
  }
  return [...byBackend.entries()].map(([backend, ms]) => [
    backend,
    kinds.map((get, i) => ({
      name: names[i],
      color: colors[i],
      value: ms.reduce((s, m) => s + get(m), 0),
    })),
  ])
}

function providerAggregates(models: ModelStat[]) {
  const byBackend = new Map<
    string,
    {
      requests: number
      successes: number
      toolCalls: number
      toolErrors: number
      statusCodes: Record<string, number>
    }
  >()
  for (const m of models) {
    const cur =
      byBackend.get(m.backend) ?? {
        requests: 0,
        successes: 0,
        toolCalls: 0,
        toolErrors: 0,
        statusCodes: {},
      }
    cur.requests += m.requests
    cur.successes += m.successes
    cur.toolCalls += m.tool_calls
    cur.toolErrors += m.tool_errors
    for (const [code, n] of Object.entries(m.status_codes ?? {})) {
      cur.statusCodes[code] = (cur.statusCodes[code] ?? 0) + n
    }
    byBackend.set(m.backend, cur)
  }
  return [...byBackend.entries()].map(([backend, v]) => ({
    backend,
    requests: v.requests,
    uptime: v.requests ? v.successes / v.requests : 0,
    toolCalls: v.toolCalls,
    toolErrors: v.toolErrors,
    statusCodes: v.statusCodes,
  }))
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
          <ThemeIcon variant="light" color="red" size="sm" radius="xl" aria-hidden="true">
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
          <Paper key={`${e.at}-${i}`} withBorder radius="md" p="xs" bg="var(--mantine-color-default-hover)">
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
          <ThemeIcon variant="light" color="brand" size="sm" radius="xl" aria-hidden="true">
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
                    <Text size="xs" c="dimmed" truncate>
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
                <Table.Th scope="col" />
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
  const isErr = status === 'error'
  const isClientError = status.startsWith('4')
  const isServerError = status.startsWith('5')
  const isSuccess = status.startsWith('2')
  const color = isErr || isServerError ? 'red' : isClientError ? 'yellow' : isSuccess ? 'teal' : 'gray'
  const icon = isErr ? <IconServerOff size={11} aria-hidden="true" />
    : isClientError || isServerError ? <IconAlertTriangle size={11} aria-hidden="true" />
      : isSuccess ? <IconCheck size={11} aria-hidden="true" /> : <IconPointFilled size={11} aria-hidden="true" />
  const description = isErr ? 'No HTTP response from upstream' : `HTTP ${status}`
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
        {isErr ? 'no response' : status}
      </Badge>
    </Tooltip>
  )
}
