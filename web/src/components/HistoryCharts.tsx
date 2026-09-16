import { AreaChart, BarChart, LineChart } from '@mantine/charts'
import { Box, Group, Paper, Stack, Table, Text } from '@mantine/core'
import { useId, type CSSProperties } from 'react'
import type { SeriesPoint } from '../api'
import { fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'

export type HistoryFormatter = (value: number) => string

export interface HistorySeries {
  name: string
  label: string
  formatter: HistoryFormatter
}

type HistoryChartData = Record<string, number | string>
type ColoredSeries = HistorySeries & { color: string; strokeDasharray?: string }

const linePatterns = ['', '6 4', '2 3', '8 3 2 3']
const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/
// Anything past 36h is the weekly view.
const DATE_AXIS_SPAN_MS = 36 * 60 * 60 * 1000
// Override Mantine's chart-local defaults with the shell's inherited tokens.
const chartStyle = {
  '--chart-grid-color': 'inherit',
  '--chart-cursor-fill': 'inherit',
  fontVariantNumeric: 'tabular-nums',
} as CSSProperties
const gridStyle = { stroke: 'var(--chart-grid-color)', strokeDasharray: '0', strokeWidth: 1 }
type ChartTooltipPayload = { dataKey?: unknown; value?: unknown }
const axisTick = { fontSize: 11, fill: 'var(--mantine-color-dimmed)' }

function mergeSeries(...groups: Array<SeriesPoint[] | undefined>): HistoryChartData[] {
  const timestamps = [
    ...new Set(groups.flatMap((group) => group ?? []).map((point) => point.ts)),
  ].sort()
  const byTs = groups.map((group) => new Map((group ?? []).map((point) => [point.ts, point.value])))
  return timestamps.map((ts) => {
    const row: HistoryChartData = { time: ts }
    byTs.forEach((values, index) => {
      const value = values.get(ts)
      // A missing sample is not a measured zero.
      if (value !== undefined && Number.isFinite(value)) row[`series${index}`] = value
    })
    return row
  })
}

function usesDateAxis(data: HistoryChartData[]): boolean {
  const times = data.map((point) => String(point.time))
  if (times.some((ts) => DATE_ONLY.test(ts))) return true
  if (times.length < 2) return false
  const first = Date.parse(times[0])
  const last = Date.parse(times[times.length - 1])
  return Number.isFinite(first) && Number.isFinite(last) && last - first >= DATE_AXIS_SPAN_MS
}

function formatAxisTime(value: string, dateAxis = false) {
  const dateOnly = DATE_ONLY.test(value)
  const date = new Date(dateOnly ? `${value}T00:00:00Z` : value)
  if (!Number.isFinite(date.getTime())) return value
  if (dateAxis || dateOnly) {
    return date.toLocaleDateString('en-US', {
      month: 'short', day: 'numeric', ...(dateOnly ? { timeZone: 'UTC' } : {}),
    })
  }
  return date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })
}

function formatTooltipTime(value: string) {
  if (DATE_ONLY.test(value)) {
    return new Date(`${value}T00:00:00Z`).toLocaleDateString('en-US', {
      month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC',
    })
  }
  const date = new Date(value)
  return Number.isFinite(date.getTime())
    ? date.toLocaleString('en-US', { timeZoneName: 'short' })
    : value
}

function axisTimeFormatter(data: HistoryChartData[]) {
  const dateAxis = usesDateAxis(data)
  return (value: string) => formatAxisTime(value, dateAxis)
}

// A zero axis tick is a baseline, even for metrics where zero means no sample.
function axisFormatter(format: HistoryFormatter) {
  return (value: number) => (Number.isFinite(value) && value !== 0 ? format(value) : '0')
}

function sampleValue(value: unknown, formatter: HistoryFormatter): number | undefined {
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
  // Duration/throughput formatters use an em dash for an unobserved histogram.
  return formatter(value) === '—' ? undefined : value
}

// Legends and tooltips key series with a short line, mirroring the mark.
function LineKey({ item }: { item: ColoredSeries }) {
  return (
    <svg width="24" height="10" aria-hidden="true" style={{ flexShrink: 0 }}>
      <line x1="0" y1="5" x2="24" y2="5" stroke={item.color} strokeWidth="2" strokeDasharray={item.strokeDasharray} />
    </svg>
  )
}

// Frosted surface above the plot; values lead, series names follow.
function ChartTooltip({ payload, timestamp, series }: {
  payload: ReadonlyArray<{ dataKey?: unknown; value?: unknown }>
  timestamp: string
  series: ColoredSeries[]
}) {
  const byName = new Map(payload.map((item) => [String(item.dataKey), item.value]))
  return (
    <Paper
      withBorder
      p={10}
      radius="md"
      style={{
        maxWidth: 'min(320px, calc(100vw - 32px))',
        backgroundColor: 'color-mix(in srgb, var(--mantine-color-default) 88%, transparent)',
        backdropFilter: 'saturate(1.8) blur(16px)',
        WebkitBackdropFilter: 'saturate(1.8) blur(16px)',
        borderColor: 'var(--mantine-color-default-border)',
      }}
    >
      <Text size="xs" c="dimmed" mb={6} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {formatTooltipTime(timestamp)}
      </Text>
      <Stack gap={4}>
        {series.map((item) => {
          const value = sampleValue(byName.get(item.name), item.formatter)
          return (
            <Group key={item.name} justify="space-between" gap="xs">
              <Group gap={6} wrap="nowrap" miw={0}>
                <LineKey item={item} />
                <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{item.label}</Text>
              </Group>
              <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
                {value === undefined ? 'No sample' : item.formatter(value)}
              </Text>
            </Group>
          )
        })}
      </Stack>
    </Paper>
  )
}

// Native disclosure and a semantic table provide touch/keyboard inspection,
// including missing intervals, without making every plotted point a tab stop.
function ChartDataTable({ title, data, series }: {
  title: string
  data: HistoryChartData[]
  series: HistorySeries[]
}) {
  return (
    <Box component="details" mt="xs" style={{ fontSize: 'var(--mantine-font-size-xs)' }}>
      <Box component="summary" py="sm" mih={44} c="dimmed" style={{ cursor: 'pointer' }}>
        View data <Text component="span" inherit style={{ fontVariantNumeric: 'tabular-nums' }}>({data.length} intervals)</Text>
      </Box>
      <Box mt={4} mah={260} style={{ overflow: 'auto' }} tabIndex={0} role="region" aria-label={`${title} data table`}>
        <Table fz="xs" striped withRowBorders>
          <Table.Caption>{title} · timestamps include timezone; — means no sample. Values are not compacted.</Table.Caption>
          <Table.Thead>
            <Table.Tr>
              <Table.Th scope="col">Time</Table.Th>
              {series.map((item) => <Table.Th scope="col" key={item.name} ta="right">{item.label}</Table.Th>)}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.map((point, index) => (
              <Table.Tr key={`${point.time}-${index}`}>
                <Table.Th scope="row" fw={400} style={{ whiteSpace: 'nowrap' }}>{formatTooltipTime(String(point.time))}</Table.Th>
                {series.map((item) => {
                  const value = sampleValue(point[item.name], item.formatter)
                  return (
                    <Table.Td key={item.name} ta="right" style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                      {value === undefined ? <span aria-label="No sample">—</span> : (
                        <span title={item.formatter(value)}>{value.toLocaleString('en-US', { maximumFractionDigits: 20 })}</span>
                      )}
                    </Table.Td>
                  )
                })}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Box>
    </Box>
  )
}

export function HistoryLineChart({ title, description, data, series, height = 132 }: {
  title: string
  description: string
  data: HistoryChartData[]
  series: HistorySeries[]
  height?: number
}) {
  const pal = useChartPalette()
  const id = useId()
  const coloredSeries = series.map((item, index) => ({
    ...item,
    color: pal.series[index] ?? pal.series[0],
    strokeDasharray: linePatterns[index % linePatterns.length],
  }))
  const plotData = data.map((point) => {
    const row: HistoryChartData = { time: point.time }
    series.forEach((item) => {
      const value = sampleValue(point[item.name], item.formatter)
      if (value !== undefined) row[item.name] = value
    })
    return row
  })
  const hasData = plotData.some((point) => series.some((item) => typeof point[item.name] === 'number'))
  const formatter = axisFormatter(series[0]?.formatter ?? fmtInt)
  // Single series reads as an area with a gradient wash; multi-series keeps
  // crisp lines so overlapping series don't muddy each other.
  const single = series.length === 1

  const chartProps = {
    h: height,
    data: plotData,
    dataKey: 'time',
    curveType: 'linear' as const,
    connectNulls: false,
    accessibilityLayer: true,
    series: coloredSeries,
    valueFormatter: formatter,
    gridProps: gridStyle,
    style: chartStyle,
    tooltipProps: {
      cursor: { fill: 'var(--chart-cursor-fill)', strokeWidth: 1, stroke: 'var(--chart-grid-color)' },
      content: ({ active, payload, label }: { active?: boolean; payload?: readonly ChartTooltipPayload[]; label?: unknown }) =>
        active && payload?.length ? (
          <ChartTooltip payload={payload} timestamp={String(label)} series={coloredSeries} />
        ) : null,
    },
    xAxisProps: {
      tick: axisTick,
      tickFormatter: axisTimeFormatter(data),
      tickLine: false as const,
      axisLine: false as const,
      minTickGap: 32,
      interval: 'preserveStartEnd' as const,
    },
    yAxisProps: {
      tick: axisTick,
      tickLine: false as const,
      axisLine: false as const,
      width: 48,
      tickCount: 4,
      tickFormatter: formatter,
    },
  }

  return (
    <Box miw={0} role="group" aria-labelledby={id} aria-describedby={`${id}-description`}>
      <Text id={id} size="sm" fw={650} lh={1.25}>{title}</Text>
      <Text id={`${id}-description`} size="xs" c="dimmed" mt={1} mb={10} lh={1.3}>{description}</Text>
      {!hasData ? (
        <Box h={height} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No recorded samples in this range</Text>
        </Box>
      ) : (
        <>
          {single ? (
            <AreaChart
              {...chartProps}
              withGradient
              fillOpacity={0.1}
              strokeWidth={2}
              strokeDasharray="0"
              areaProps={{ strokeLinecap: 'round', strokeLinejoin: 'round' }}
              dotProps={{ r: 4, strokeWidth: 2, stroke: 'var(--mantine-color-body)' }}
              activeDotProps={{ r: 5, fill: coloredSeries[0].color, strokeWidth: 2, stroke: 'var(--mantine-color-body)' }}
            />
          ) : (
            <LineChart
              {...chartProps}
              strokeWidth={2}
              strokeDasharray="0"
              lineProps={{ strokeLinecap: 'round', strokeLinejoin: 'round' }}
              dotProps={{ r: 4, strokeWidth: 2, stroke: 'var(--mantine-color-body)' }}
              activeDotProps={{ r: 5, strokeWidth: 2, stroke: 'var(--mantine-color-body)' }}
            />
          )}
          {coloredSeries.length > 1 && (
            <Group gap="xs" justify="center" mt={4} aria-label="Chart legend">
              {coloredSeries.map((item) => (
                <Group key={item.name} gap={6} wrap="nowrap" miw={0}>
                  <LineKey item={item} />
                  <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{item.label}</Text>
                </Group>
              ))}
            </Group>
          )}
          <ChartDataTable title={title} data={data} series={series} />
        </>
      )}
    </Box>
  )
}

export function HistoryBarChart({ title, description, points, formatter = fmtInt, color, height = 112 }: {
  title: string
  description: string
  points: SeriesPoint[]
  formatter?: HistoryFormatter
  color?: string
  height?: number
}) {
  const pal = useChartPalette()
  const id = useId()
  const barSeries = { name: 'value', label: title, formatter, color: color ?? pal.series[2] }
  const data = points.map((point): HistoryChartData => {
    const value = sampleValue(point.value, formatter)
    return value === undefined ? { time: point.ts } : { time: point.ts, value }
  })
  const hasData = data.some((point) => typeof point.value === 'number')
  return (
    <Box miw={0} role="group" aria-labelledby={id} aria-describedby={`${id}-description`}>
      <Text id={id} size="sm" fw={650} lh={1.25}>{title}</Text>
      <Text id={`${id}-description`} size="xs" c="dimmed" mt={1} mb={10} lh={1.3}>{description}</Text>
      {!hasData ? (
        <Box h={height} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No recorded samples in this range</Text>
        </Box>
      ) : (
        <>
          <BarChart
            style={chartStyle}
            h={height}
            data={data}
            dataKey="time"
            accessibilityLayer
            series={[barSeries]}
            valueFormatter={axisFormatter(formatter)}
            gridProps={gridStyle}
            tooltipProps={{
              cursor: { fill: 'var(--chart-cursor-fill)' },
              content: ({ active, payload, label }) => active && payload?.length ? (
                <ChartTooltip payload={payload} timestamp={String(label)} series={[barSeries]} />
              ) : null,
            }}
            xAxisProps={{ tick: axisTick, tickFormatter: axisTimeFormatter(data), tickLine: false, axisLine: false, minTickGap: 32, interval: 'preserveStartEnd' }}
            yAxisProps={{ tick: axisTick, tickLine: false, axisLine: false, width: 48, tickCount: 4, tickFormatter: axisFormatter(formatter) }}
            barProps={{ radius: [4, 4, 0, 0], maxBarSize: 18 }}
            gridAxis="y"
            strokeDasharray="0"
          />
          <ChartDataTable title={title} data={data} series={[barSeries]} />
        </>
      )}
    </Box>
  )
}

export function historyData(groups: Array<SeriesPoint[] | undefined>): HistoryChartData[] {
  return mergeSeries(...groups)
}

export const historyFormatters = {
  count: fmtInt,
  percent: (value: number) => value === 0 ? '0.0%' : fmtPct(value),
  seconds: fmtSec,
  tps: fmtTps,
}
