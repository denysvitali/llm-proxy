import { Card, Code, SimpleGrid, Skeleton, Text } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { SeriesPoint, StatsSeriesResponse } from '../../api'
import { PageSection } from '../PageSection'
import { TimeRangeControl } from '../TimeRangeControl'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import ErrorRetryCard from '../ErrorRetryCard'

// Four always-visible charts: volume, reliability, latency, tokens.
// No tab switching, so every key signal is on screen at once.
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

function ChartCard({ title, headline, caption, children }: { title: string; headline: string; caption: string; children: React.ReactNode }) {
  return (
    <Card withBorder radius="lg" p="lg" className="overview-chart-card">
      <div className="overview-chart-headline" aria-label={title}><span className="tabular">{headline}</span><Text size="xs" c="dimmed">{caption}</Text></div>
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
  const tokensIn = series?.tokens_in?.reduce((sum, p) => sum + p.value, 0) ?? 0
  const tokensOut = series?.tokens_out?.reduce((sum, p) => sum + p.value, 0) ?? 0
  const e2e = lastValue(series?.e2e_p50)
  const ttft = lastValue(series?.ttft_p50)
  const tps = lastValue(series?.throughput_p50)
  const latency = historyData([series?.ttft_p50, series?.e2e_p50])
  const tokens = historyData([series?.tokens_in, series?.tokens_out])
  const successData = historyData([series?.success_rate])

  return (
    <PageSection title="Performance over time"
      description={query.isError ? 'History unavailable' : requestCount === undefined ? 'Volume, reliability, latency and tokens' : `${requestCount.toLocaleString('en-US')} requests in range`}
      extra={<TimeRangeControl value={range} onChange={onRangeChange} />}>
      {query.isPending ? <SimpleGrid cols={{ base: 1, md: 2 }}>{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} height={280} radius="lg" />)}</SimpleGrid> : query.isError ? (
        <ErrorRetryCard title="Couldn't load time history"
          message={<>History is unavailable. Check connectivity and whether <Code>stats.persist_file</Code> or Redis history is configured.</>}
          onRetry={() => query.refetch({ cancelRefetch: false })} retrying={query.isFetching} />
      ) : (
        <div className="overview-history-grid">
          <ChartCard title="Requests" headline={requestCount === undefined ? '—' : historyFormatters.count(requestCount)} caption="upstream attempts in range">
            <HistoryBarChart title="Request volume" description="Upstream attempts per interval" points={requests ?? []} height={180} />
          </ChartCard>
          <ChartCard title="Success rate" headline={successRate === undefined ? '—' : historyFormatters.percent(successRate)} caption="request-weighted average">
            <HistoryLineChart title="Success rate" description="Share of attempts that succeeded" data={successData}
              series={[{ name: 'series0', label: 'Success', formatter: historyFormatters.percent }]} height={180} />
          </ChartCard>
          <ChartCard title="Latency" headline={e2e === undefined ? '—' : historyFormatters.seconds(e2e)} caption={ttft === undefined ? 'median full response, latest' : `median full response · ${historyFormatters.seconds(ttft)} to first byte`}>
            <HistoryLineChart title="Latency" description="Median time to first byte and full response" data={latency}
              series={[{ name: 'series0', label: 'First byte', formatter: historyFormatters.seconds }, { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds }]} height={180} />
          </ChartCard>
          <ChartCard title="Tokens" headline={historyFormatters.count(tokensIn + tokensOut)} caption={`${historyFormatters.count(tokensIn)} in · ${historyFormatters.count(tokensOut)} out${tps === undefined ? '' : ` · ${historyFormatters.tps(tps)} tok/s`}`}>
            <HistoryLineChart title="Token volume" description="Input and output tokens per interval" data={tokens}
              series={[{ name: 'series0', label: 'Input', formatter: historyFormatters.count }, { name: 'series1', label: 'Output', formatter: historyFormatters.count }]} height={180} />
          </ChartCard>
        </div>
      )}
    </PageSection>
  )
}
