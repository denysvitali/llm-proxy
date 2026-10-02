import { useMemo, useState } from 'react'
import { Card, Code, Group, SegmentedControl, SimpleGrid, Skeleton, Stack, Text } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { StatsSeriesResponse } from '../../api'
import { PageSection } from '../PageSection'
import { TimeRangeControl } from '../TimeRangeControl'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import ErrorRetryCard from '../ErrorRetryCard'

export default function OverviewHistory({ query, range, onRangeChange }: {
  query: UseQueryResult<StatsSeriesResponse, Error>
  range: string
  onRangeChange: (range: string) => void
}) {
  const [metric, setMetric] = useState('latency')
  const series = query.data?.series
  const history = useMemo(() => ({
    latency: historyData([series?.ttft_p50, series?.e2e_p50]),
    throughput: historyData([series?.throughput_p50]),
    tokens: historyData([series?.tokens_in, series?.tokens_out]),
  }), [series])
  const requests = series?.requests
  const requestCount = requests?.length ? Math.round(requests.reduce((sum, point) => sum + point.value, 0)) : undefined

  return (
    <PageSection title="Performance over time"
      description={query.isError ? 'History unavailable' : requestCount === undefined ? 'Explore request volume and response performance' : `${requestCount.toLocaleString('en-US')} requests in range`}
      extra={<TimeRangeControl value={range} onChange={onRangeChange} />}>
      {query.isPending ? <SimpleGrid cols={{ base: 1, md: 2 }}><Skeleton height={320} radius="lg" /><Skeleton height={320} radius="lg" /></SimpleGrid> : query.isError ? (
        <ErrorRetryCard title="Couldn't load time history"
          message={<>History is unavailable. Check connectivity and whether <Code>stats.persist_file</Code> or Redis history is configured.</>}
          onRetry={() => query.refetch({ cancelRefetch: false })} retrying={query.isFetching} />
      ) : (
        <div className="overview-history-grid">
          <Card withBorder radius="lg" p="lg" className="overview-chart-card">
            <HistoryBarChart title="Request volume" description="Upstream attempts per recorded interval"
              points={requests ?? []} height={208} />
          </Card>
          <Card withBorder radius="lg" p="lg" className="overview-chart-card">
            <Group justify="space-between" align="center" mb="md" gap="xs">
              <Text size="xs" fw={600} c="dimmed">RESPONSE PERFORMANCE</Text>
              <SegmentedControl aria-label="Performance metric" size="xs" value={metric} onChange={setMetric}
                data={[{ value: 'latency', label: 'Latency' }, { value: 'throughput', label: 'Speed' }, { value: 'tokens', label: 'Tokens' }]} />
            </Group>
            <Stack gap={0}>
              {metric === 'latency' ? <HistoryLineChart title="Latency" description="Median time to first byte and full response" data={history.latency}
                series={[{ name: 'series0', label: 'First byte', formatter: historyFormatters.seconds }, { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds }]}
                height={154} /> : metric === 'throughput' ? <HistoryLineChart title="Throughput" description="Median output tokens per second" data={history.throughput}
                  series={[{ name: 'series0', label: 'Tokens/sec', formatter: historyFormatters.tps }]} height={154} /> : <HistoryLineChart title="Token volume" description="Input and output tokens per recorded interval" data={history.tokens}
                    series={[{ name: 'series0', label: 'Input', formatter: historyFormatters.count }, { name: 'series1', label: 'Output', formatter: historyFormatters.count }]} height={154} />}
            </Stack>
          </Card>
        </div>
      )}
    </PageSection>
  )
}
