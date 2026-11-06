import { AreaChart, BarChart, LineChart } from '@mantine/charts'
import { Box, Group, Paper, Stack, Table, Text } from '@mantine/core'
import { memo, useMemo, useId, useState, type CSSProperties, type ReactNode } from 'react'
import { ReferenceDot } from 'recharts'
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
// Above this many samples a per-point dot turns a line into a scatter plot.
const MAX_SAMPLE_DOTS = 12
// Override Mantine's chart-local defaults with the shell's inherited tokens.
const chartStyle = {
  '--chart-grid-color': 'inherit',
  '--chart-cursor-fill': 'inherit',
  fontVariantNumeric: 'tabular-nums',
} as CSSProperties
// Hand-drawn calm: a recessive, only-horizontal dashed grid. Vertical rules
// and axis lines are noise at 150px tall.
const gridStyle = { stroke: 'var(--chart-grid-color)', strokeDasharray: '3 4', strokeWidth: 1 }
type ChartTooltipPayload = { dataKey?: unknown; value?: unknown }
const axisTick = { fontSize: 11, fill: 'var(--mantine-color-dimmed)' }
// Keep Mantine's axis offsets but at the smaller type scale.
const xTick = { ...axisTick, transform: 'translate(0, 8)' }
const yTick = { ...axisTick, transform: 'translate(-6, 0)' }

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

interface SeriesDelta {
  pct: number
  direction: 'up' | 'down' | 'flat'
}

// Change from the first to the latest SAMPLED value. Undefined when the series
// has fewer than two samples or the baseline is zero — never invent a delta.
export function seriesDelta(values: number[]): SeriesDelta | undefined {
  const samples = values.filter((value) => Number.isFinite(value))
  if (samples.length < 2) return undefined
  const first = samples[0]
  const last = samples[samples.length - 1]
  if (first === 0) return undefined
  const pct = ((last - first) / Math.abs(first)) * 100
  if (!Number.isFinite(pct)) return undefined
  return { pct, direction: pct > 0 ? 'up' : pct < 0 ? 'down' : 'flat' }
}

const pillStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 3,
  padding: '1px 6px',
  borderRadius: 999,
  background: 'var(--sunken)',
  flexShrink: 0,
  fontVariantNumeric: 'tabular-nums',
}

// A compact delta pill: arrow + percent change from the first sample. Near
// zero stays muted so noise never reads as a trend.
function DeltaPill({ delta }: { delta: SeriesDelta }) {
  const nearZero = Math.abs(delta.pct) < 0.5
  const arrow = delta.direction === 'up' ? '▲' : delta.direction === 'down' ? '▼' : '±'
  const label = `${delta.pct > 0 ? '+' : ''}${delta.pct.toFixed(1)}%`
  return (
    <span
      style={{ ...pillStyle, color: nearZero ? 'var(--muted)' : 'var(--ink)' }}
      title={`Change from the first to the latest sampled value: ${label}`}
      aria-label={`Change from first to latest sample: ${label}`}
    >
      <span aria-hidden="true" style={{ fontSize: 9 }}>{arrow}</span>
      <span style={{ fontSize: 11, fontWeight: 600 }}>{label}</span>
    </span>
  )
}

// Legends and tooltips key series with a short line, mirroring the mark.
function LineKey({ item }: { item: ColoredSeries }) {
  return (
    <svg width="22" height="10" aria-hidden="true" style={{ flexShrink: 0 }}>
      <line x1="0" y1="5" x2="22" y2="5" stroke={item.color} strokeWidth={1.6} strokeDasharray={item.strokeDasharray} />
    </svg>
  )
}

// Opaque elevated surface above the plot; values lead, series names follow.
// Opaque rather than frosted: the tooltip sits over dense rows, and blur there
// costs number legibility for an effect the hairline language doesn't use.
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
        backgroundColor: 'var(--card)',
        borderColor: 'var(--hairline)',
        boxShadow: '0 8px 24px rgba(10, 10, 10, 0.10)',
        fontVariantNumeric: 'tabular-nums',
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
  const [open, setOpen] = useState(false)
  return (
    <Box component="details" mt="xs" style={{ fontSize: 'var(--mantine-font-size-xs)' }}>
      <Box component="summary" py="sm" mih={44} c="dimmed" style={{ cursor: 'pointer' }} onClick={() => setOpen((v) => !v)}>
        View data <Text component="span" inherit style={{ fontVariantNumeric: 'tabular-nums' }}>({data.length} intervals)</Text>
      </Box>
      {open && (
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
      )}
    </Box>
  )
}

const assistiveOnly: CSSProperties = {
  position: 'absolute',
  width: 1,
  height: 1,
  padding: 0,
  margin: 0,
  overflow: 'hidden',
  clipPath: 'inset(50%)',
  whiteSpace: 'nowrap',
  border: 0,
}

export const HistoryLineChart = memo(function HistoryLineChart({ title, description, data, series, height = 132, compact = false }: {
  title: string
  description: string
  data: HistoryChartData[]
  series: HistorySeries[]
  height?: number
  // Overview cards already show a headline. Hide this chart's own title,
  // description, and single-series legend visually; keep the names for AT.
  compact?: boolean
}) {
  const pal = useChartPalette()
  const id = useId()
  // Series colors are assigned by the caller's stable series ORDER, which is
  // the fixed categorical slot order (input, output, cache read, cache write).
  // Callers must pass their series in that canonical order so a series keeps its
  // hue when a chart is filtered — color follows the entity, not its rank
  // (DESIGN.md §5). A caller that reorders its array would repaint the series.
  // pal is NOT in the dep arrays: useChartPalette() returns a fresh object on
  // every render, so including it would defeat the memo. Colors depend on the
  // series array (index → slot) and the scheme, which is stable per session.
  const palSeries = pal.series
  const coloredSeries = useMemo(() => series.map((item, index) => ({
    ...item,
    color: palSeries[index] ?? palSeries[0],
    strokeDasharray: linePatterns[index % linePatterns.length],
  })), [series, palSeries])
  const plotData = useMemo(() => data.map((point) => {
    const row: HistoryChartData = { time: point.time }
    series.forEach((item) => {
      const value = sampleValue(point[item.name], item.formatter)
      if (value !== undefined) row[item.name] = value
    })
    return row
  }), [data, series])
  // Latest sampled value and point per series, plus the change vs the first
  // sample. Keeping only observed samples is what makes "No sample" honest.
  const seriesStats = useMemo(() => {
    const stats = new Map<string, { lastIndex: number; lastValue: number; delta?: SeriesDelta }>()
    series.forEach((item) => {
      const values: number[] = []
      let lastIndex = -1
      for (let i = 0; i < data.length; i++) {
        const value = sampleValue(data[i][item.name], item.formatter)
        if (value !== undefined) {
          values.push(value)
          lastIndex = i
        }
      }
      if (lastIndex < 0) return
      stats.set(item.name, {
        lastIndex,
        lastValue: values[values.length - 1],
        delta: seriesDelta(values),
      })
    })
    return stats
  }, [data, series])
  const hasData = plotData.some((point) => series.some((item) => typeof point[item.name] === 'number'))
  const sampleCount = plotData.filter((point) => series.some((item) => typeof point[item.name] === 'number')).length
  const formatter = axisFormatter(series[0]?.formatter ?? fmtInt)
  // Single series reads as an area with a gradient wash; multi-series keeps
  // crisp lines so overlapping series don't muddy each other. A percentage
  // series is excluded: a near-1.0 rate fills the whole plot and the wash
  // reads as a translucent band, not data.
  const single = series.length === 1
  const percentScale = single && series[0]?.formatter === historyFormatters.percent
  const useArea = single && !percentScale
  const showDots = sampleCount > 0 && sampleCount <= MAX_SAMPLE_DOTS

  // A single small terminal dot marks the newest sample when the series is
  // too dense for per-point dots.
  const terminalDots: ReactNode = !showDots && hasData
    ? coloredSeries.map((item) => {
      const stat = seriesStats.get(item.name)
      if (!stat) return null
      const point = data[stat.lastIndex]
      if (!point) return null
      return (
        <ReferenceDot
          key={item.name}
          x={point.time}
          y={sampleValue(point[item.name], item.formatter)}
          r={2.5}
          fill={item.color}
          stroke="var(--card)"
          strokeWidth={1.5}
          pointerEvents="none"
        />
      )
    })
    : null

  const chartProps = {
    h: height,
    data: plotData,
    dataKey: 'time',
    curveType: 'monotone' as const,
    connectNulls: false,
    accessibilityLayer: true,
    series: coloredSeries,
    valueFormatter: formatter,
    withLegend: false,
    withDots: showDots,
    gridAxis: 'y' as const,
    gridProps: gridStyle,
    style: chartStyle,
    tooltipProps: {
      cursor: { fill: 'var(--chart-cursor-fill)', strokeWidth: 1, stroke: 'var(--chart-grid-color)', strokeDasharray: '3 4' },
      content: ({ active, payload, label }: { active?: boolean; payload?: readonly ChartTooltipPayload[]; label?: unknown }) =>
        active && payload?.length ? (
          <ChartTooltip payload={payload} timestamp={String(label)} series={coloredSeries} />
        ) : null,
    },
    xAxisProps: {
      tick: xTick,
      tickFormatter: axisTimeFormatter(data),
      tickLine: false as const,
      axisLine: false as const,
      minTickGap: 32,
      interval: 'preserveStartEnd' as const,
    },
    yAxisProps: {
      tick: yTick,
      tickLine: false as const,
      axisLine: false as const,
      width: 44,
      tickCount: 4,
      ...(single && (series[0]?.formatter === fmtPct || percentScale) ? { domain: [0, 1] as [number, number], ticks: [0, 0.5, 1] } : {}),
      tickFormatter: formatter,
    },
  }

  return (
    <Box miw={0} pos="relative" role="group" aria-labelledby={id} aria-describedby={`${id}-description`}>
      <Text id={id} size="sm" fw={650} lh={1.25} style={compact ? assistiveOnly : undefined}>{title}</Text>
      <Text id={`${id}-description`} size="xs" c="dimmed" mt={compact ? 0 : 1} mb={compact ? 0 : 10} lh={1.3} style={compact ? assistiveOnly : undefined}>{description}</Text>
      {hasData && (!compact || !single) && (
        <Group gap={compact ? 'sm' : 'md'} mb="sm" aria-label={compact ? 'Chart legend' : 'Latest sampled values and chart legend'}>
          {coloredSeries.map((item) => {
            const stat = seriesStats.get(item.name)
            const point = stat ? data[stat.lastIndex] : undefined
            // Compact cards carry the values in their headline; the legend
            // only has to name the marks so it stays on one line.
            return <Group key={item.name} gap={6} wrap="nowrap" miw={0} title={point ? `Latest sample: ${formatTooltipTime(String(point.time))}` : 'No sample'}>
              <LineKey item={item} />
              <Text size="xs" c="dimmed">{item.label}</Text>
              {!compact && <>
                <Text size="xs" fw={600} className="tabular">{stat ? item.formatter(stat.lastValue) : '—'}</Text>
                <Text size="xs" c="dimmed">latest</Text>
                {stat?.delta && <DeltaPill delta={stat.delta} />}
              </>}
            </Group>
          })}
        </Group>
      )}
      {!hasData ? (
        <Box h={height} style={{ display: 'grid', placeItems: 'center' }}>
          <Text size="sm" c="dimmed" ta="center">No recorded samples in this range</Text>
        </Box>
      ) : (
        <>
          {useArea ? (
            <AreaChart
              {...chartProps}
              withGradient
              fillOpacity={0.1}
              strokeWidth={1.6}
              areaProps={{ strokeLinecap: 'round', strokeLinejoin: 'round' }}
              dotProps={{ r: 2.5, strokeWidth: 0, pointerEvents: 'none' }}
              activeDotProps={{ r: 3, strokeWidth: 0, pointerEvents: 'none' }}
              areaChartProps={{ margin: { top: 6, right: 4, bottom: 0, left: 0 } }}
            >
              {terminalDots}
            </AreaChart>
          ) : (
            <LineChart
              {...chartProps}
              strokeWidth={1.6}
              lineProps={(item) => ({
                strokeLinecap: 'round',
                strokeLinejoin: 'round',
                strokeDasharray: (item as ColoredSeries).strokeDasharray,
              })}
              dotProps={{ r: 2.5, strokeWidth: 0, pointerEvents: 'none' }}
              activeDotProps={{ r: 3, strokeWidth: 0, pointerEvents: 'none' }}
              lineChartProps={{ margin: { top: 6, right: 4, bottom: 0, left: 0 } }}
            >
              {terminalDots}
            </LineChart>
          )}
          <ChartDataTable title={title} data={data} series={series} />
        </>
      )}
    </Box>
  )
})

export const HistoryBarChart = memo(function HistoryBarChart({ title, description, points, formatter = fmtInt, color, height = 112, compact = false }: {
  title: string
  description: string
  points: SeriesPoint[]
  formatter?: HistoryFormatter
  color?: string
  height?: number
  compact?: boolean
}) {
  const pal = useChartPalette()
  const id = useId()
  // One measure, one color: the default is the single magnitude hue, not a
  // categorical slot. A caller may pass `color` to tie the bars to a specific
  // entity (e.g. a per-model hue), but an unpassed bar chart must never borrow
  // a series color, which would read as "this is the cache-read series".
  const barSeries = { name: 'value', label: title, formatter, color: color ?? pal.magnitude }
  const data = points.map((point): HistoryChartData => {
    const value = sampleValue(point.value, formatter)
    return value === undefined ? { time: point.ts } : { time: point.ts, value }
  })
  const hasData = data.some((point) => typeof point.value === 'number')
  const observed = data.map((point) => point.value).filter((value): value is number => typeof value === 'number')
  const delta = seriesDelta(observed)
  return (
    <Box miw={0} pos="relative" role="group" aria-labelledby={id} aria-describedby={`${id}-description`}>
      {compact ? (
        <Text id={id} size="sm" fw={650} lh={1.25} style={assistiveOnly}>{title}</Text>
      ) : (
        <Group justify="space-between" align="center" gap="xs" wrap="nowrap">
          <Text id={id} size="sm" fw={650} lh={1.25}>{title}</Text>
          {hasData && delta && <DeltaPill delta={delta} />}
        </Group>
      )}
      <Text id={`${id}-description`} size="xs" c="dimmed" mt={compact ? 0 : 1} mb={compact ? 0 : 10} lh={1.3} style={compact ? assistiveOnly : undefined}>{description}</Text>
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
            gridAxis="y"
            gridProps={gridStyle}
            tooltipProps={{
              cursor: { fill: 'var(--chart-cursor-fill)' },
              content: ({ active, payload, label }) => active && payload?.length ? (
                <ChartTooltip payload={payload} timestamp={String(label)} series={[barSeries]} />
              ) : null,
            }}
            xAxisProps={{ tick: xTick, tickFormatter: axisTimeFormatter(data), tickLine: false, axisLine: false, minTickGap: 32, interval: 'preserveStartEnd' }}
            yAxisProps={{ tick: yTick, tickLine: false, axisLine: false, width: 44, tickCount: 4, tickFormatter: axisFormatter(formatter) }}
            barProps={{ radius: [3, 3, 0, 0], maxBarSize: 18 }}
            barChartProps={{ margin: { top: 6, right: 4, bottom: 0, left: 0 } }}
          />
          <ChartDataTable title={title} data={data} series={[barSeries]} />
        </>
      )}
    </Box>
  )
})

export function historyData(groups: Array<SeriesPoint[] | undefined>): HistoryChartData[] {
  return mergeSeries(...groups)
}

export const historyFormatters = {
  count: fmtInt,
  percent: (value: number) => value === 0 ? '0.0%' : fmtPct(value),
  seconds: fmtSec,
  tps: fmtTps,
}
