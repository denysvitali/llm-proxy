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

function formatAxisTime(value: string) {
  return new Date(value).toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
  })
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
      <Text size="xs" c="dimmed" mb={6}>
        {new Date(timestamp).toLocaleString('en-US')}
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
}: {
  title: string
  description: string
  data: HistoryChartData[]
  series: HistorySeries[]
}) {
  const pal = useChartPalette()
  const coloredSeries = series.map((item, index) => ({
    ...item,
    color: pal.series[chartColors[index] ?? index % pal.series.length],
  }))
  const hasData = data.some((point) =>
    coloredSeries.some((item) => Number(point[item.name] ?? 0) !== 0),
  )

  return (
    <Box>
      <Text size="sm" fw={650} lh={1.25}>{title}</Text>
      <Text size="xs" c="dimmed" mt={1} mb={10} lh={1.3}>{description}</Text>
      {!hasData ? (
        <Box h={150} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">
            No traffic in this range
          </Text>
        </Box>
      ) : (
        <LineChart
          h={132}
          data={data}
          dataKey="time"
          curveType="linear"
          connectNulls
          series={coloredSeries.map(({ name, label, color }) => ({ name, label, color }))}
          valueFormatter={(value) => coloredSeries[0].formatter(Number(value))}
          tooltipProps={{
            content: ({ active, payload, label }) =>
              active && payload?.length ? (
                <ChartTooltip payload={payload} timestamp={String(label)} series={coloredSeries} />
              ) : null,
          }}
          xAxisProps={{
            tickFormatter: formatAxisTime,
            tickLine: false,
            axisLine: false,
            minTickGap: 24,
          }}
          yAxisProps={{ tickLine: false, axisLine: false, width: 42 }}
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
}: {
  title: string
  description: string
  points: SeriesPoint[]
  formatter?: HistoryFormatter
  color?: string
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
        <Box h={130} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No traffic in this range</Text>
        </Box>
      ) : (
        <BarChart
          h={112}
          data={data}
          dataKey="time"
          series={[{ name: 'value', label: title, color: color ?? pal.series[2] }]}
          valueFormatter={formatter}
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
            tickFormatter: formatAxisTime,
            tickLine: false,
            axisLine: false,
            minTickGap: 24,
          }}
          yAxisProps={{ tickLine: false, axisLine: false, width: 42 }}
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
