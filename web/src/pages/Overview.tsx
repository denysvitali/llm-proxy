import { useState } from 'react'
import {
  Box,
  Alert,
  Badge,
  Card,
  Code,
  Divider,
  Group,
  Loader,
  Progress,
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
  IconCoin,
  IconCoins,
  IconShieldCheck,
  IconTool,
  IconServerOff,
} from '@tabler/icons-react'
import { BarChart, LineChart } from '@mantine/charts'
import { useMediaQuery } from '@mantine/hooks'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'
import { fetchGrokUsage, fetchOverview, fetchStats, fetchStatsSeries, fetchUpstreamErrors } from '../api'
import { useLiveStatsUpdates } from '../useLiveUpdates'
import type { GrokUsage, ModelStat, SeriesPoint, UpstreamErrorEvent } from '../api'
import { clampRate, fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import { SegmentedControl } from '@mantine/core'
import StatTile from '../components/StatTile'
import StatusChips from '../components/StatusChips'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { Fade } from '../App'

export default function OverviewPage() {
  const liveConnected = useLiveStatsUpdates()
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

  const latencyData = mergeSeries(seriesQ.data?.series.ttft_p50, seriesQ.data?.series.e2e_p50)
  const throughputData = seriesQ.data?.series.throughput_p50.map(toChartData) ?? []
  const volumeData = mergeSeries(seriesQ.data?.series.tokens_in, seriesQ.data?.series.tokens_out)
  const requestCount = sumPoints(seriesQ.data?.series.requests)
  const latencySeries = [
    { name: 'series0', label: 'First byte', color: pal.series[0], formatter: fmtSec },
    { name: 'series1', label: 'Full response', color: pal.series[1], formatter: fmtSec },
  ]
  const throughputSeries = [{ name: 'value', label: 'Tokens/sec', color: pal.series[2], formatter: fmtTps }]
  const volumeSeries = [
    { name: 'series0', label: 'Input', color: pal.series[0], formatter: fmtInt },
    { name: 'series1', label: 'Output', color: pal.series[1], formatter: fmtInt },
  ]
  const hasTraffic =
    latencyData.some((point) => Number(point.series0) > 0 || Number(point.series1) > 0) ||
    throughputData.some((point) => Number(point.value) > 0) ||
    volumeData.some((point) => Number(point.series0) > 0 || Number(point.series1) > 0)

  return (
    <Fade fetching={statsQ.isFetching || ovQ.isFetching || errorsQ.isFetching}>
      <Stack gap="lg">
        <Title order={4} mb={-6}>
          Fleet overview
        </Title>

        {errorsQ.data && errorsQ.data.errors.length > 0 && (
          <UpstreamErrorsCard errors={errorsQ.data.errors} />
        )}

        <Card withBorder radius="lg" p="md">
          <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={12}>
            <div>
              <Title order={5}>Performance over time</Title>
              <Text size="xs" c="dimmed">
                {seriesQ.isError ? 'History unavailable' : `${requestCount.toLocaleString('en-US')} requests in range`}
              </Text>
            </div>
            <Badge variant="light" color={liveConnected ? 'teal' : 'orange'} size="sm">
              {liveConnected ? 'Live' : 'Reconnecting'}
            </Badge>
            <SegmentedControl
              value={range}
              onChange={setRange}
              data={[
                { value: '1h', label: '1h' },
                { value: '6h', label: '6h' },
                { value: '24h', label: '24h' },
                { value: '7d', label: '7d' },
              ]}
            />
          </Group>

          {seriesQ.isPending ? (
            <Group justify="center" py="xl"><Loader size="sm" /></Group>
          ) : seriesQ.isError ? (
            <Text c="dimmed" py="xl" ta="center">
              Time history requires <Code>stats.persist_file</Code> to be configured.
            </Text>
          ) : (
            <SimpleGrid cols={{ base: 1, lg: 3 }} spacing="lg">
              <TimeChart
                title="Latency"
                description="Median first byte and full response"
                data={latencyData}
                series={latencySeries}
                empty={!hasTraffic}
              />
              <TimeChart
                title="Throughput"
                description="Median output rate"
                data={throughputData}
                series={throughputSeries}
                empty={!hasTraffic}
              />
              <TimeChart
                title="Token volume"
                description="Input and output tokens"
                data={volumeData}
                series={volumeSeries}
                empty={!hasTraffic}
              />
            </SimpleGrid>
          )}
        </Card>

        {grokUsageEnabled && <GrokUsageCard query={grokUsageQ} />}

        {statsQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : models.length === 0 ? (
          <Text c="dimmed" py="xl" ta="center">
            No model traffic recorded yet — send a request through the proxy and it will show up here.
          </Text>
        ) : (
          <>
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

            <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="lg">
              <Card withBorder radius="lg" p="md">
                <Title order={5} mb="sm">
                  Requests by model
                </Title>
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
              </Card>

              <Card withBorder radius="lg" p="md">
                <Title order={5} mb="sm">
                  Token mix by provider
                </Title>
                <Stack gap="md">
                  {providerSegments(models, pal.series).map(([backend, segs]) => (
                    <Box key={backend}>
                      <Group justify="space-between" mb={4}>
                        <Text size="sm">{backend}</Text>
                      </Group>
                      <TokenMixBar segments={segs} />
                      <TokenLegend segments={segs} />
                    </Box>
                  ))}
                </Stack>
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
          </>
        )}
        {ovQ.data && (
          <Text size="xs" c="dimmed">
            {ovQ.data.name} v{ovQ.data.version} · listening on {ovQ.data.listen} · auth{' '}
            {ovQ.data.authEnabled ? 'enabled' : 'disabled'}
          </Text>
        )}
      </Stack>
    </Fade>
  )
}

function GrokUsageCard({ query }: { query: UseQueryResult<GrokUsage, Error> }) {
  const usage = query.data
  const error = query.error
  const percent = usage?.hasPercent ? Math.max(0, Math.min(100, usage.percentUsed)) : null
  const color = percent === null ? 'blue' : percent >= 90 ? 'red' : percent >= 70 ? 'yellow' : 'teal'

  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb={usage?.hasPercent ? 10 : 0}>
        <div>
          <Title order={5}>Grok subscription</Title>
          <Text size="xs" c="dimmed">
            {usage?.subscriptionTier || 'xAI coding subscription'}
            {usage?.email ? ` · ${usage.email}` : ''}
          </Text>
        </div>
        {query.isFetching ? <Loader size="xs" /> : null}
      </Group>

      {query.isPending ? (
        <Group justify="center" py="md"><Loader size="sm" /></Group>
      ) : error ? (
        <Alert color="red" variant="light" title="Usage unavailable">
          {sanitizeGrokError(error.message)}
        </Alert>
      ) : !usage?.hasPercent ? (
        <Text c="dimmed">No billing data is available for this account.</Text>
      ) : (
        <>
          <Group justify="space-between" align="baseline" mb={6}>
            <Text fz={28} fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
              {usage.percentUsed.toFixed(1)}%
            </Text>
            <Text size="sm" c="dimmed">used this period</Text>
          </Group>
          <Progress value={percent ?? 0} color={color} size="lg" radius="sm" aria-label="Grok subscription used" />
          <Group justify="space-between" mt={6}>
            <Text size="xs" c="dimmed">{formatPeriod(usage.periodStart, usage.periodEnd)}</Text>
            <Text size="xs" c="dimmed">Updated {new Date(usage.fetchedAt).toLocaleTimeString('en-US', { hour12: false })}</Text>
          </Group>
          <Divider my="md" />
          <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="md">
            <UsageMoney label="Included limit" cents={usage.limitCents} />
            <UsageMoney label="Used" cents={usage.usedCents} />
            <UsageMoney label="Remaining" cents={usage.remainingCents} />
            <UsageMoney label="Prepaid" cents={usage.prepaidCents} />
          </SimpleGrid>
          {(usage.onDemandUsedCents != null || usage.onDemandCapCents != null) && (
            <Group gap={6} mt="sm">
              <IconCoin size={15} stroke={1.8} />
              <Text size="xs" c="dimmed">
                Extra usage {formatMoney(usage.onDemandUsedCents)}
                {usage.onDemandCapCents != null ? ` of ${formatMoney(usage.onDemandCapCents)}` : ''}
              </Text>
            </Group>
          )}
        </>
      )}
    </Card>
  )
}

function UsageMoney({ label, cents }: { label: string; cents?: number }) {
  return (
    <div>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>{label}</Text>
      <Text fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMoney(cents)}</Text>
    </div>
  )
}

function formatMoney(cents?: number) {
  if (cents == null || !Number.isFinite(cents)) return '—'
  return (Math.abs(cents) / 100).toLocaleString('en-US', { style: 'currency', currency: 'USD' })
}

function formatPeriod(start?: string, end?: string) {
  if (!start && !end) return 'Current period'
  const startDate = start ? new Date(start) : undefined
  const endDate = end ? new Date(end) : undefined
  const validStart = startDate && !Number.isNaN(startDate.getTime())
  const validEnd = endDate && !Number.isNaN(endDate.getTime())
  if (validStart && validEnd) return `${startDate!.toLocaleDateString()} – ${endDate!.toLocaleDateString()}`
  if (validEnd) return `Resets ${endDate!.toLocaleString()}`
  if (validStart) return `Started ${startDate!.toLocaleDateString()}`
  return 'Current period'
}

function sanitizeGrokError(message: string) {
  const decoded = message.startsWith('/api/grok/usage: ') ? message.slice('/api/grok/usage: '.length) : message
  return decoded || 'Usage information is temporarily unavailable.'
}

interface TimeSeriesEntry {
  name: string
  label: string
  color: string
  formatter: (value: number) => string
}

type TimeChartData = Record<string, number | string>

function TimeChart({
  title,
  description,
  data,
  series,
  empty,
}: {
  title: string
  description: string
  data: TimeChartData[]
  series: TimeSeriesEntry[]
  empty?: boolean
}) {
  return (
    <Box>
      <Group justify="space-between" align="baseline" wrap="nowrap" gap="xs" mb={4}>
        <Text size="sm" fw={600}>{title}</Text>
      </Group>
      <Text size="xs" c="dimmed" mb={8}>{description}</Text>

      {empty ? (
        <Box h={180} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No traffic in this range</Text>
        </Box>
      ) : (
        <LineChart
          h={180}
          data={data}
          dataKey="time"
          curveType="monotone"
          connectNulls
          series={series.map((item) => ({ name: item.name, label: item.label, color: item.color }))}
          valueFormatter={(value) => series[0].formatter(value)}
          tooltipProps={{
            content: ({ active, payload, label }) =>
              active && payload?.length ? (
                <ChartTooltip payload={payload} timestamp={String(label)} series={series} />
              ) : null,
          }}
          xAxisProps={{
            tickFormatter: formatAxisTime,
            tickLine: false,
            axisLine: false,
            minTickGap: 28,
          }}
          yAxisProps={{ tickLine: false, axisLine: false, width: 52 }}
          gridAxis="y"
          tooltipAnimationDuration={150}
        />
      )}
    </Box>
  )
}

function toChartData(point: SeriesPoint): TimeChartData {
  return { time: point.ts, value: point.value }
}

function ChartTooltip({
  payload,
  timestamp,
  series,
}: {
  payload: ReadonlyArray<{ dataKey?: unknown; value?: unknown }>
  timestamp: string
  series: TimeSeriesEntry[]
}) {
  const byName = new Map(payload.map((item) => [String(item.dataKey), Number(item.value ?? 0)]))
  return (
    <Paper withBorder p={10} radius="md" shadow="sm" style={{ minWidth: 150 }}>
      <Text size="xs" c="dimmed" mb={6}>{new Date(timestamp).toLocaleString('en-US')}</Text>
      <Stack gap={4}>
        {series.map((item) => (
          <Group key={item.name} justify="space-between" wrap="nowrap" gap="md">
            <Box style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span style={{ width: 8, height: 8, borderRadius: 999, background: item.color }} />
              <Text size="xs">{item.label}</Text>
            </Box>
            <Text size="xs" fw={600}>{item.formatter(byName.get(item.name) ?? 0)}</Text>
          </Group>
        ))}
      </Stack>
    </Paper>
  )
}

function mergeSeries(...groups: Array<SeriesPoint[] | undefined>): TimeChartData[] {
  const timestamps = [...new Set(groups.flatMap((group) => group ?? []).map((point) => point.ts))].sort()
  const byTs = groups.map((group) => new Map((group ?? []).map((point) => [point.ts, point.value])))
  return timestamps.map((ts) => {
    const row: TimeChartData = { time: ts }
    byTs.forEach((values, index) => {
      row[`series${index}`] = values.get(ts) ?? 0
    })
    return row
  })
}

function sumPoints(points?: SeriesPoint[]) {
  return Math.round((points ?? []).reduce((sum, point) => sum + point.value, 0))
}

function formatAxisTime(value: string) {
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    return new Date(`${value}T00:00:00Z`).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  }
  return new Date(value).toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })
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
  const shown = errors.slice(0, 8)
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
