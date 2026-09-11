import { useState } from 'react'
import {
  Box,
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
} from '@mantine/core'
import {
  IconActivity,
  IconAlertTriangle,
  IconBolt,
  IconCoins,
  IconShieldCheck,
  IconTool,
  IconServerOff,
} from '@tabler/icons-react'
import { BarChart } from '@mantine/charts'
import { useMediaQuery } from '@mantine/hooks'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { fetchGrokUsage, fetchOverview, fetchRequest, fetchRequests, fetchStats, fetchStatsSeries, fetchUpstreamErrors, fetchZcodeUsage } from '../api'
import { useLiveStatsUpdates } from '../useLiveUpdates'
import type { InspectedRequest, ModelStat, SeriesPoint, UpstreamErrorEvent } from '../api'
import GrokUsageCard from '../components/GrokUsageCard'
import ZcodeUsageCard from '../components/ZcodeUsageCard'
import { PageHeader } from '../components/PageHeader'
import { TimeRangeControl } from '../components/TimeRangeControl'
import { clampRate, fmtInt, fmtPct, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import StatTile from '../components/StatTile'
import StatusChips from '../components/StatusChips'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { HistoryLineChart, historyData, historyFormatters } from '../components/HistoryCharts'
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
    placeholderData: keepPreviousData,
  })
  const errorsQ = useQuery({
    queryKey: ['upstream-errors'],
    queryFn: fetchUpstreamErrors,
    refetchInterval: 30_000,
    retry: 1,
  })
  const requestsQ = useQuery({ queryKey: ['recent-requests'], queryFn: fetchRequests, refetchInterval: 30_000 })

  const models = statsQ.data?.models ?? []
  const pal = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  const totalRequests = models.reduce((s, m) => s + m.requests, 0)
  const totalSuccess = models.reduce((s, m) => s + m.successes, 0)
  const tokensIn = models.reduce((s, m) => s + m.input_tokens, 0)
  const tokensOut = models.reduce((s, m) => s + m.output_tokens, 0)
  const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
  const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)

  // Requests-weighted median throughput across models: the honest aggregate
  // of per-model p50s we can compute without the raw histogram buckets.
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
      model: m.model.length > labelMax ? `${m.model.slice(0, labelMax - 1)}…` : m.model,
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

        <SimpleGrid cols={{ base: 2, sm: 3, lg: 6 }} spacing="md">
          <StatTile
            label="Requests"
            value={fmtInt(totalRequests)}
            hint={`${fmtInt(totalSuccess)} succeeded`}
            icon={<IconActivity size={16} />}
            accent="brand"
          />
          <StatTile
            label="Uptime"
            value={fmtPct(totalRequests ? totalSuccess / totalRequests : 0)}
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
            value={fmtPct(clampRate(toolCalls ? toolErrors / toolCalls : 0))}
            hint={`${fmtInt(toolErrors)} errored · ${fmtInt(toolCalls)} calls`}
            icon={<IconAlertTriangle size={16} />}
            accent={toolErrors > 0 ? 'red' : 'gray'}
          />
        </SimpleGrid>

        {models.length === 0 && !statsQ.isPending && (
          <Text c="dimmed" size="sm">
            No model traffic recorded yet — send a request through the proxy and it will show up here.
          </Text>
        )}

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

        <Card withBorder radius="lg" p="md">
          <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={12}>
            <div>
              <Title order={5}>Performance over time</Title>
              <Text size="xs" c="dimmed">
                {seriesQ.isError ? 'History unavailable' : `${requestCount.toLocaleString('en-US')} requests in range`}
              </Text>
            </div>
            <TimeRangeControl value={range} onChange={setRange} />
          </Group>

          {seriesQ.isPending ? (
            <Group justify="center" py="xl"><Loader size="sm" /></Group>
          ) : seriesQ.isError ? (
            <Text c="dimmed" py="xl" ta="center">
              Time history requires <Code>stats.persist_file</Code> to be configured.
            </Text>
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

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="lg">
          <Card withBorder radius="lg" p="md">
            <Title order={5} mb="sm">
              Requests by model
            </Title>
            {topModels.length === 0 ? (
              <Text size="sm" c="dimmed">No model traffic yet</Text>
            ) : (
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
                yAxisProps={{ width: isMobile ? 122 : 176, tickLine: false }}
                tooltipAnimationDuration={150}
              />
            )}
          </Card>

          <Card withBorder radius="lg" p="md">
            <Title order={5} mb="sm">
              Token mix by provider
            </Title>
            {mix.length === 0 ? (
              <Text size="sm" c="dimmed">No provider traffic yet</Text>
            ) : (
              <Stack gap="md">
                {mix.map(([backend, segs]) => (
                  <Box key={backend}>
                    <Group justify="space-between" mb={4}>
                      <Text size="sm">{backend}</Text>
                    </Group>
                    <TokenMixBar segments={segs} />
                    <TokenLegend segments={segs} />
                  </Box>
                ))}
              </Stack>
            )}
          </Card>
        </SimpleGrid>

        <Card withBorder radius="lg" p="md">
          <Title order={5} mb="sm">
            Provider uptime
          </Title>
          <ScrollArea>
            <Table verticalSpacing="xs" horizontalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Backend</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th ta="right">Uptime</Table.Th>
                  {!isMobile && <Table.Th>Upstream errors</Table.Th>}
                  {!isMobile && <Table.Th ta="right">Requests</Table.Th>}
                  {!isMobile && <Table.Th ta="right">Tool err</Table.Th>}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {providerAggregates(models).map((p) => (
                  <Table.Tr key={p.backend}>
                    <Table.Td>{p.backend}</Table.Td>
                    <Table.Td>
                      <UptimeBadge uptime={p.uptime} requests={p.requests} />
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                      {fmtPct(p.uptime)}
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
                        {p.toolCalls ? fmtPct(p.toolErrors / p.toolCalls) : '—'}
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Card>

        <RecentRequestsCard requests={requestsQ.data?.requests ?? []} loading={requestsQ.isPending} isMobile={isMobile} />

        {errorsQ.data && errorsQ.data.errors.length > 0 && (
          <UpstreamErrorsCard errors={errorsQ.data.errors} />
        )}
      </Stack>
    </Fade>
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
          <ThemeIcon variant="light" color="red" size="sm" radius="xl">
            <IconServerOff size={13} />
          </ThemeIcon>
          <Title order={5}>Recent upstream errors</Title>
        </Group>
        <Text size="xs" c="dimmed">
          {errors.length === 1 ? '1 failure' : `${fmtInt(errors.length)} failures · newest first`}
        </Text>
      </Group>
      <Stack gap={6}>
        {shown.map((e, i) => (
          <Paper key={`${e.at}-${i}`} withBorder radius="md" p="xs" bg="var(--mantine-color-default-hover)">
            <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
              <Box style={{ minWidth: 0 }}>
                <Group gap={6} wrap="nowrap">
                  <StatusBadge status={e.status} />
                  <Text size="xs" c="dimmed" style={{ fontVariantNumeric: 'tabular-nums' }}>
                    {formatEventTime(e.at)}
                  </Text>
                </Group>
                <Text size="sm" fw={500} mt={4} truncate>
                  {e.backend} / {e.model}
                </Text>
                {e.message && (
                  <Text size="xs" c="dimmed" lineClamp={2} mt={2}>
                    {e.message}
                  </Text>
                )}
                {e.request_id && <RequestInspectButton id={e.request_id} />}
              </Box>
            </Group>
          </Paper>
        ))}
        {errors.length > shown.length && (
          <Text size="xs" c="dimmed">…and {fmtInt(errors.length - shown.length)} older</Text>
        )}
      </Stack>
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
      <Group justify="space-between" mb={10}>
        <Title order={5}>Recent requests</Title>
        <Text size="xs" c="dimmed">
          {requests.length > shown.length
            ? `Newest ${shown.length} of ${fmtInt(requests.length)}`
            : `Last ${fmtInt(requests.length)} upstream attempts`}
        </Text>
      </Group>
      {loading ? (
        <Group justify="center" py="sm"><Loader size="sm" /></Group>
      ) : requests.length === 0 ? (
        <Text size="sm" c="dimmed">No requests captured since this proxy instance started.</Text>
      ) : isMobile ? (
        <Stack gap={6}>
          {shown.map((request) => (
            <Paper key={request.id} withBorder radius="md" p="xs">
              <Group justify="space-between" align="center" wrap="nowrap" gap="xs">
                <Box style={{ minWidth: 0 }}>
                  <Text size="xs" c="dimmed" truncate>
                    {formatEventTime(request.at)} · {request.backend} / {request.model}
                  </Text>
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
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Time</Table.Th>
                <Table.Th>Backend / model</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {shown.map((request) => (
                <Table.Tr key={request.id}>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{formatEventTime(request.at)}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{request.backend} / {request.model}</Text>
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
    <Button size="compact-xs" variant="subtle" onClick={() => setOpened(true)}>Inspect</Button>
    <Modal opened={opened} onClose={() => setOpened(false)} title="Request inspection" size="xl">
      {query.isPending ? <Group justify="center" py="xl"><Loader size="sm" /></Group> : query.isError ? (
        <Alert color="red">This request is no longer available. The in-memory history keeps the latest 50 attempts per instance.</Alert>
      ) : query.data ? <RequestDetail request={query.data} /> : null}
    </Modal>
  </>
}

function RequestDetail({ request }: { request: InspectedRequest }) {
  return <Stack gap="sm">
    <Group gap="xs"><StatusBadge status={request.status} /><Text size="sm">{request.backend} / {request.model}</Text></Group>
    <Text size="xs" c="dimmed">Proxy request ID: {request.proxy_request_id || 'unavailable'} · wire format: {request.kind || 'unknown'}</Text>
    {request.error && <Alert color="red" variant="light">{request.error}</Alert>}
    <Alert color="blue" variant="light">
      Request payloads are not retained by the proxy, so prompts, tool inputs, and credentials cannot appear in dashboard history.
    </Alert>
  </Stack>
}

function StatusBadge({ status }: { status: string }) {
  const isErr = status === 'error'
  const color = isErr ? 'red' : status.startsWith('5') ? 'red' : status.startsWith('4') ? 'yellow' : 'gray'
  return (
    <Badge size="sm" variant="light" color={color} styles={{ root: { fontWeight: 700 } }}>
      {isErr ? 'no response' : status}
    </Badge>
  )
}

function formatEventTime(ts: string) {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString('en-US', { hour12: false })
}
