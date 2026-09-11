import { BarChart, LineChart } from '@mantine/charts'
import { Box, Group, Paper, Stack, Text } from '@mantine/core'
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

const chartColors = [0, 1, 2]
const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/
// 24h series spans ~1 day; 7d spans ~7. Anything past 36h is the weekly view.
const DATE_AXIS_SPAN_MS = 36 * 60 * 60 * 1000

function mergeSeries(...groups: Array<SeriesPoint[] | undefined>): HistoryChartData[] {
  const timestamps = [
    ...new Set(groups.flatMap((group) => group ?? []).map((point) => point.ts)),
  ].sort()
  const byTs = groups.map((group) => new Map((group ?? []).map((point) => [point.ts, point.value])))
  return timestamps.map((ts) => {
    const row: HistoryChartData = { time: ts }
    byTs.forEach((values, index) => {
      row[`series${index}`] = values.get(ts) ?? 0
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
  if (dateAxis || DATE_ONLY.test(value)) {
    const date = DATE_ONLY.test(value) ? new Date(`${value}T00:00:00Z`) : new Date(value)
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  }
  return new Date(value).toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
  })
}

function formatTooltipTime(value: string) {
  if (DATE_ONLY.test(value)) {
    return new Date(`${value}T00:00:00Z`).toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
    })
  }
  return new Date(value).toLocaleString('en-US')
}

function axisTimeFormatter(data: HistoryChartData[]) {
  const dateAxis = usesDateAxis(data)
  return (value: string) => formatAxisTime(value, dateAxis)
}

// Axis ticks must never read "—": a zero tick is the baseline itself, and
// formatters like fmtSec render 0 as an em-dash ("no data"), which on an
// axis reads as a broken chart. Wrap any formatter for axis use.
function axisFormatter(format: HistoryFormatter) {
  return (value: number) => (Number.isFinite(value) && value !== 0 ? format(value) : '0')
}

function ChartTooltip({
  payload,
  timestamp,
  series,
}: {
  payload: ReadonlyArray<{ dataKey?: unknown; value?: unknown }>
  timestamp: string
  series: (HistorySeries & { color: string })[]
}) {
  const byName = new Map(payload.map((item) => [String(item.dataKey), Number(item.value ?? 0)]))
  return (
    <Paper withBorder p={10} radius="md" shadow="sm" style={{ minWidth: 150 }}>
      <Text size="xs" c="dimmed" mb={6} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {formatTooltipTime(timestamp)}
      </Text>
      <Stack gap={4}>
        {series.map((item) => (
          <Group key={item.name} justify="space-between" wrap="nowrap" gap="md">
            <Box style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span
                style={{ width: 8, height: 8, borderRadius: 999, background: item.color }}
              />
              <Text size="xs">{item.label}</Text>
            </Box>
            <Text size="xs" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
              {item.formatter(byName.get(item.name) ?? 0)}
            </Text>
          </Group>
        ))}
      </Stack>
    </Paper>
  )
}

export function HistoryLineChart({
  title,
  description,
  data,
  series,
  height = 132,
}: {
  title: string
  description: string
  data: HistoryChartData[]
  series: HistorySeries[]
  height?: number
}) {
  const pal = useChartPalette()
  const coloredSeries = series.map((item, index) => ({
    ...item,
    color: pal.series[chartColors[index] ?? index % pal.series.length],
  }))
  const hasData = data.some((point) =>
    coloredSeries.some((item) => Number(point[item.name] ?? 0) !== 0),
  )
  // A single series is named by its title — no legend box. With two or more,
  // the legend carries identity so the reader never has to match by color.
  const withLegend = coloredSeries.length > 1

  return (
    <Box>
      <Text size="sm" fw={650} lh={1.25}>{title}</Text>
      <Text size="xs" c="dimmed" mt={1} mb={10} lh={1.3}>{description}</Text>
      {!hasData ? (
        <Box h={height} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">
            No traffic in this range
          </Text>
        </Box>
      ) : (
        <LineChart
          h={height}
          data={data}
          dataKey="time"
          curveType="monotone"
          connectNulls
          withLegend={withLegend}
          // Bottom-center: `position: 'left'` sat the swatches on the y-axis.
          legendProps={{ verticalAlign: 'bottom', align: 'center', wrapperStyle: { paddingTop: 8 } } as never}
          series={coloredSeries.map(({ name, label, color }) => ({ name, label, color }))}
          valueFormatter={axisFormatter(coloredSeries[0].formatter)}
          tooltipProps={{
            content: ({ active, payload, label }) =>
              active && payload?.length ? (
                <ChartTooltip payload={payload} timestamp={String(label)} series={coloredSeries} />
              ) : null,
          }}
          xAxisProps={{
            tickFormatter: axisTimeFormatter(data),
            tickLine: false,
            axisLine: false,
            minTickGap: 24,
          }}
          yAxisProps={{
            tickLine: false,
            axisLine: false,
            width: 48,
            tickFormatter: axisFormatter(coloredSeries[0].formatter),
          }}
          gridAxis="y"
          tooltipAnimationDuration={150}
          dotProps={{ r: 2, strokeWidth: 0 }}
          activeDotProps={{ r: 4, strokeWidth: 2, stroke: 'var(--mantine-color-body)' }}
        />
      )}
    </Box>
  )
}

export function HistoryBarChart({
  title,
  description,
  points,
  formatter = fmtInt,
  color,
  height = 112,
}: {
  title: string
  description: string
  points: SeriesPoint[]
  formatter?: HistoryFormatter
  color?: string
  height?: number
}) {
  const pal = useChartPalette()
  const barSeries: HistorySeries = { name: 'value', label: title, formatter }
  const data = points.map((point) => ({
    time: point.ts,
    value: point.value,
  }))
  return (
    <Box>
      <Text size="sm" fw={650} lh={1.25}>{title}</Text>
      <Text size="xs" c="dimmed" mt={1} mb={10} lh={1.3}>{description}</Text>
      {data.every((point) => point.value === 0) ? (
        <Box h={height} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No traffic in this range</Text>
        </Box>
      ) : (
        <BarChart
          h={height}
          data={data}
          dataKey="time"
          series={[{ name: 'value', label: title, color: color ?? pal.series[2] }]}
          valueFormatter={axisFormatter(formatter)}
          tooltipProps={{
            content: ({ active, payload, label }) => {
              if (!active || !payload?.length) return null
              return (
                <ChartTooltip
                  payload={payload}
                  timestamp={String(label)}
                  series={[{ ...barSeries, color: color ?? pal.series[2] }]}
                />
              )
            },
          }}
          xAxisProps={{
            tickFormatter: axisTimeFormatter(data),
            tickLine: false,
            axisLine: false,
            minTickGap: 24,
          }}
          yAxisProps={{
            tickLine: false,
            axisLine: false,
            width: 48,
            tickFormatter: axisFormatter(formatter),
          }}
          barProps={{ radius: [3, 3, 0, 0], maxBarSize: 18 }}
          gridAxis="y"
        />
      )}
    </Box>
  )
}

export function historyData(
  groups: Array<SeriesPoint[] | undefined>,
): HistoryChartData[] {
  return mergeSeries(...groups)
}

export const historyFormatters = {
  count: fmtInt,
  percent: (value: number) => fmtPct(value),
  seconds: fmtSec,
  tps: fmtTps,
}
