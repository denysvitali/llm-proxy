import { Card, Code, Skeleton } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { SeriesPoint, StatsSeriesResponse } from '../../api'
import { PageSection } from '../PageSection'
import { TimeRangeControl } from '../TimeRangeControl'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import ErrorRetryCard from '../ErrorRetryCard'

// Four always-visible charts: volume, reliability, latency, tokens.
// No tab switching, so every key signal is on screen at once. Each card reads
// as one instrument: micro-label measure, tabular headline, a caption naming
// the statistic and range, and a local delta chip against the previous
// interval computed from the same series that feeds the plot.
function weighted(points: SeriesPoint[] | undefined, weights: SeriesPoint[] | undefined): number | undefined {
  if (!points?.length) return undefined
  const w = new Map((weights ?? []).map((p) => [p.ts, p.value]))
  let sum = 0
  let total = 0
  for (const p of points) {
    const weight = w.get(p.ts) ?? 1
    if (Number.isFinite(p.value) && weight > 0) { sum += p.value * weight; total += weight }
  }
  return total ? sum / total : undefined
}

function lastValue(points: SeriesPoint[] | undefined): number | undefined {
  const valid = (points ?? []).filter((p) => Number.isFinite(p.value) && p.value > 0)
  return valid.length ? valid[valid.length - 1].value : undefined
}

// The two most recent finite samples of a series. Idle intervals are masked to
// NaN upstream; they are skipped, never treated as a measured zero.
function lastTwoFinite(points: SeriesPoint[] | undefined): [number, number] | undefined {
  const finite = (points ?? []).filter((p) => Number.isFinite(p.value))
  if (finite.length < 2) return undefined
  return [finite[finite.length - 2].value, finite[finite.length - 1].value]
}

// Per-interval sum of two series on shared timestamps (tokens in + out).
function sumSeries(a: SeriesPoint[] | undefined, b: SeriesPoint[] | undefined): SeriesPoint[] | undefined {
  if (!a?.length && !b?.length) return undefined
  const byTs = new Map<string, number>()
  for (const point of [...(a ?? []), ...(b ?? [])]) {
    if (Number.isFinite(point.value)) byTs.set(point.ts, (byTs.get(point.ts) ?? 0) + point.value)
  }
  return [...byTs.entries()].sort(([x], [y]) => x.localeCompare(y)).map(([ts, value]) => ({ ts, value }))
}

interface Delta {
  direction: 'up' | 'down'
  percent: number
  previous: number
  latest: number
  tone?: 'good' | 'bad'
}

// Percentage change against the previous interval. Skipped when either sample
// is missing or the previous value is zero (an undefined multiple, not a fact).
function trend(points: SeriesPoint[] | undefined, lowerIsBetter = false, toneByDirection = false): Delta | undefined {
  const pair = lastTwoFinite(points)
  if (!pair) return undefined
  const [previous, latest] = pair
  if (previous === 0) return undefined
  const percent = ((latest - previous) / Math.abs(previous)) * 100
  if (!Number.isFinite(percent) || percent === 0) return undefined
  const direction = percent > 0 ? 'up' : 'down'
  const tone = toneByDirection ? (direction === 'up' ? (lowerIsBetter ? 'bad' : 'good') : (lowerIsBetter ? 'good' : 'bad')) : undefined
  return { direction, percent: Math.abs(percent), previous, latest, tone }
}

function rangeLabel(range: string) {
  return range === '1h' ? 'last hour' : range === '7d' ? 'last 7 days' : range === '6h' ? 'last 6 hours' : `last ${range}`
}

function ChartCard({ title, headline, caption, delta, format, children }: {
  title: string
  headline: string
  caption: string
  delta?: Delta
  format?: (value: number) => string
  children: ReactNode
}) {
  return (
    <Card p="md" miw={0} className="overview-chart-card">
      <div className="overview-chart-head">
        <div className="overview-chart-titles" aria-label={title}>
          <span className="overview-label">{title}</span>
          <span className="overview-chart-headline">{headline}</span>
          <span className="overview-chart-caption">{caption}</span>
        </div>
        {delta && (
          <span
            className="overview-chart-delta"
            data-tone={delta.tone}
            title={`Previous interval ${format ? format(delta.previous) : delta.previous.toLocaleString('en-US')} → latest ${format ? format(delta.latest) : delta.latest.toLocaleString('en-US')}`}
            aria-label={`${delta.direction === 'up' ? 'Up' : 'Down'} ${delta.percent.toFixed(1)} percent versus the previous interval`}
          >
            <span aria-hidden="true">{delta.direction === 'up' ? '▲' : '▼'}</span>
            {delta.percent >= 100 ? Math.round(delta.percent) : delta.percent.toFixed(1)}%
          </span>
        )}
      </div>
      {children}
    </Card>
  )
}

export default function OverviewHistory({ query, range, onRangeChange }: {
  query: UseQueryResult<StatsSeriesResponse, Error>
  range: string
  onRangeChange: (range: string) => void
}) {
  const series = query.data?.series
  const requests = series?.requests
  const requestCount = requests?.length ? Math.round(requests.reduce((sum, point) => sum + point.value, 0)) : undefined
  const successRate = weighted(series?.success_rate, requests)
  const tokenTotal = (points: SeriesPoint[] | undefined) => {
    const samples = points?.filter((point) => Number.isFinite(point.value)) ?? []
    return samples.length ? samples.reduce((sum, point) => sum + point.value, 0) : undefined
  }
  const tokensIn = tokenTotal(series?.tokens_in)
  const tokensOut = tokenTotal(series?.tokens_out)
  const tokenCount = tokensIn === undefined && tokensOut === undefined ? undefined : (tokensIn ?? 0) + (tokensOut ?? 0)
  const tokenLabel = (value: number | undefined) => value === undefined ? '—' : historyFormatters.count(value)
  const e2e = lastValue(series?.e2e_p50)
  const ttft = lastValue(series?.ttft_p50)
  const tps = lastValue(series?.throughput_p50)
  const latency = historyData([series?.ttft_p50, series?.e2e_p50])
  const tokens = historyData([series?.tokens_in, series?.tokens_out])
  const successData = historyData([series?.success_rate])
  const rangeText = rangeLabel(range)

  const requestDelta = trend(requests)
  const successDelta = trend(series?.success_rate, false, true)
  const latencyDelta = trend(series?.e2e_p50, true, true)
  const tokenDelta = trend(sumSeries(series?.tokens_in, series?.tokens_out))

  return (
    <PageSection title="Performance over time"
      description={query.isError ? 'History unavailable' : requestCount === undefined ? 'Volume, reliability, latency and tokens' : `${requestCount.toLocaleString('en-US')} requests in range`}
      extra={<TimeRangeControl value={range} onChange={onRangeChange} />}>
      {query.isPending ? (
        <div className="overview-chart-grid" aria-hidden="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Card key={i} p="md" className="overview-chart-card">
              <Skeleton height={11} width={70} radius="sm" />
              <Skeleton height={26} width={132} radius="sm" mt={10} mb={8} />
              <Skeleton height={12} width={110} radius="sm" mb={14} />
              <Skeleton height={150} radius="sm" />
            </Card>
          ))}
        </div>
      ) : query.isError ? (
        <ErrorRetryCard title="Couldn't load time history"
          message={<>History is unavailable. Check connectivity and whether <Code>stats.persist_file</Code> or Redis history is configured.</>}
          onRetry={() => query.refetch({ cancelRefetch: false })} retrying={query.isFetching} />
      ) : (
        <div className="overview-chart-grid">
          <ChartCard title="Requests"
            headline={requestCount === undefined ? '—' : historyFormatters.count(requestCount)}
            caption={`attempts across the ${rangeText}`}
            delta={requestDelta} format={historyFormatters.count}>
            <HistoryBarChart title="Request volume" description="Upstream attempts per interval" points={requests ?? []} height={150} compact />
          </ChartCard>
          <ChartCard title="Success rate"
            headline={successRate === undefined ? '—' : historyFormatters.percent(successRate)}
            caption={`weighted across the ${rangeText}`}
            delta={successDelta} format={historyFormatters.percent}>
            <HistoryLineChart title="Success rate" description="Share of attempts that succeeded" data={successData}
              series={[{ name: 'series0', label: 'Success', formatter: historyFormatters.percent }]} height={150} compact />
          </ChartCard>
          <ChartCard title="Latency"
            headline={e2e === undefined ? '—' : historyFormatters.seconds(e2e)}
            caption={`p50 full response across the ${rangeText}${ttft === undefined ? '' : ` · ${historyFormatters.seconds(ttft)} to first byte`}`}
            delta={latencyDelta} format={historyFormatters.seconds}>
            <HistoryLineChart title="Latency" description="Median time to first byte and full response" data={latency}
              series={[{ name: 'series0', label: 'First byte', formatter: historyFormatters.seconds }, { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds }]} height={150} compact />
          </ChartCard>
          <ChartCard title="Tokens"
            headline={tokenLabel(tokenCount)}
            caption={`${tokenLabel(tokensIn)} in · ${tokenLabel(tokensOut)} out${tps === undefined ? '' : ` · ${historyFormatters.tps(tps)} tok/s`}`}
            delta={tokenDelta} format={historyFormatters.count}>
            <HistoryLineChart title="Token volume" description="Input and output tokens per interval" data={tokens}
              series={[{ name: 'series0', label: 'Input', formatter: historyFormatters.count }, { name: 'series1', label: 'Output', formatter: historyFormatters.count }]} height={150} compact />
          </ChartCard>
        </div>
      )}
    </PageSection>
  )
}
