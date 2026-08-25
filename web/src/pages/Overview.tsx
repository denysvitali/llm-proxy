import { useState } from 'react'
import {
  Box,
  Card,
  Code,
  Group,
  Loader,
  Paper,
  ScrollArea,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import {
  IconActivity,
  IconAlertTriangle,
  IconBolt,
  IconCoins,
  IconShieldCheck,
  IconTool,
} from '@tabler/icons-react'
import { BarChart, LineChart } from '@mantine/charts'
import { useMediaQuery } from '@mantine/hooks'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { fetchOverview, fetchStats, fetchStatsSeries } from '../api'
import type { ModelStat, SeriesPoint } from '../api'
import { fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import { SegmentedControl } from '@mantine/core'
import StatTile from '../components/StatTile'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { Fade } from '../App'

export default function OverviewPage() {
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const [range, setRange] = useState('24h')
  const seriesQ = useQuery({
    queryKey: ['stats-series', range],
    queryFn: () => fetchStatsSeries(range),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
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
    <Fade fetching={statsQ.isFetching || ovQ.isFetching}>
      <Stack gap="lg">
        <Title order={4} mb={-6}>
          Fleet overview
        </Title>

        <Card withBorder radius="lg" p="md">
          <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={12}>
            <div>
              <Title order={5}>Performance over time</Title>
              <Text size="xs" c="dimmed">
                {seriesQ.isError ? 'History unavailable' : `${requestCount.toLocaleString('en-US')} requests in range`}
              </Text>
            </div>
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
                value={fmtPct(toolCalls ? toolErrors / toolCalls : 0)}
                hint="errored / calls"
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
    { requests: number; successes: number; toolCalls: number; toolErrors: number }
  >()
  for (const m of models) {
    const cur = byBackend.get(m.backend) ?? { requests: 0, successes: 0, toolCalls: 0, toolErrors: 0 }
    cur.requests += m.requests
    cur.successes += m.successes
    cur.toolCalls += m.tool_calls
    cur.toolErrors += m.tool_errors
    byBackend.set(m.backend, cur)
  }
  return [...byBackend.entries()].map(([backend, v]) => ({
    backend,
    requests: v.requests,
    uptime: v.requests ? v.successes / v.requests : 0,
    toolCalls: v.toolCalls,
    toolErrors: v.toolErrors,
  }))
}
