import type { ReactNode } from 'react'
import {
  Alert,
  Box,
  Button,
  Group,
  Loader,
  Paper,
  Progress,
  SimpleGrid,
  Stack,
  Tabs,
  Text,
  Title,
} from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import type { ModelStat, StatsSeries } from '../../api'
import { clampRate, fmtInt, fmtSec, fmtTps } from '../../format'
import { tpsSeries, tokenMixSeries } from '../../lib/chartSeries'
import { mixSegments } from '../../lib/stats'
import PercentileBars from '../PercentileBars'
import StatusChips from '../StatusChips'
import TokenMixBar, { TokenLegend } from '../TokenMixBar'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import { TimeRangeControl } from '../TimeRangeControl'
import { observedRate } from './modelData'

export default function ModelDetail({
  stat,
  colors,
  series,
  historyPending,
  historyError,
  historyFetching,
  onRetryHistory,
  range,
  onRangeChange,
}: {
  stat: ModelStat
  colors: string[]
  series?: StatsSeries
  historyPending?: boolean
  historyError?: boolean
  historyFetching?: boolean
  onRetryHistory?: () => void
  range: string
  onRangeChange: (value: string) => void
}) {
  const segs = mixSegments(stat, colors)
  const totalTok = segs.reduce((s, x) => s + x.value, 0)
  // TTFT and E2E share one time scale so their bar lengths are directly
  // comparable; throughput keeps its own scale (different unit).
  const latMax = Math.max(0, ...[stat.ttft_seconds.p99, stat.e2e_seconds.p99].filter(Number.isFinite))
  const successRate =
    stat.requests > 0 && Number.isFinite(stat.successes) ? clampRate(stat.successes / stat.requests) : NaN

  return (
    <Stack gap="lg">
      <DetailSection title="All recorded traffic">
        <SimpleGrid cols={2} spacing="md">
          <DetailStat
            label="Requests"
            value={fmtInt(stat.requests)}
            hint={`${observedRate(successRate, stat.requests)} succeeded`}
          />
          <DetailStat
            label="Latency p50"
            value={fmtSec(stat.e2e_seconds.p50)}
            hint={`TTFT ${fmtSec(stat.ttft_seconds.p50)}`}
          />
          <DetailStat
            label="Throughput"
            value={fmtTps(stat.throughput_tps.p50)}
            hint={`p90 ${fmtTps(stat.throughput_tps.p90)}`}
          />
          <DetailStat
            label="Tool error rate"
            value={observedRate(stat.tool_error_rate, stat.tool_calls)}
            hint={`${fmtInt(stat.tool_errors)} errored · ${fmtInt(stat.tool_calls)} calls`}
          />
        </SimpleGrid>
      </DetailSection>
      <Tabs defaultValue="metrics" keepMounted={false}>
        <Tabs.List grow>
          <Tabs.Tab value="metrics">Performance & reliability</Tabs.Tab>
          <Tabs.Tab value="history">History</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="metrics" pt="md">
          <Stack gap="md">
            <DetailSection title="Latency">
              <Text size="xs" fw={600} c="dimmed" mb={4}>
                Time to first token
              </Text>
              <PercentileBars values={stat.ttft_seconds} unit="s" max={latMax} />
              <Text size="xs" fw={600} c="dimmed" mt="xs" mb={4}>
                End-to-end
              </Text>
              <PercentileBars values={stat.e2e_seconds} unit="s" max={latMax} />
              <Text size="xs" c="dimmed" mt={6}>
                Both share one time scale — bar lengths compare directly.
              </Text>
            </DetailSection>
            <DetailSection title="Throughput (tokens/sec)">
              <PercentileBars values={stat.throughput_tps} unit="tok/s" />
            </DetailSection>
            <DetailSection
              title={`Tokens · ${fmtInt(totalTok)} total · cache hit ${observedRate(stat.cache_rate, stat.input_tokens + stat.cache_read_tokens + stat.cache_write_tokens)}`}
            >
              <TokenMixBar segments={segs} height={20} />
              <TokenLegend segments={segs} showPercent />
            </DetailSection>
            <DetailSection title="Reliability">
              <DetailStat label="Success rate" value={observedRate(successRate, stat.requests)} />
              {Number.isFinite(successRate) ? (
                <Progress
                  aria-label="Request success rate"
                  value={successRate * 100}
                  radius="sm"
                  size="sm"
                  color={successRate >= 0.99 ? 'teal' : successRate >= 0.9 ? 'yellow' : 'red'}
                />
              ) : (
                <Text size="sm" c="dimmed">
                  No request success observations yet.
                </Text>
              )}
              <SimpleGrid cols={2} spacing="md">
                <DetailStat label="Successful" value={fmtInt(stat.successes)} />
                <DetailStat label="Failed" value={fmtInt(stat.requests - stat.successes)} />
              </SimpleGrid>
              {Object.keys(stat.status_codes ?? {}).length > 0 && (
                <Box>
                  <Text size="xs" c="dimmed" fw={600} mb={6}>
                    Upstream errors by status
                  </Text>
                  <StatusChips codes={stat.status_codes} limit={8} />
                </Box>
              )}
            </DetailSection>
          </Stack>
        </Tabs.Panel>
        <Tabs.Panel value="history" pt="md">
          <Stack gap="md">
            <Group justify="space-between" align="center" wrap="wrap" gap="sm">
              <Text size="xs" tt="uppercase" fw={700} c="dimmed" style={{ letterSpacing: '0.04em' }}>
                History range
              </Text>
              <TimeRangeControl value={range} onChange={onRangeChange} disabled={historyPending} />
            </Group>
            <Text size="xs" c="dimmed">
              The range applies to history charts only. Summary stats and percentile bars use all recorded
              traffic.
            </Text>

            {historyPending ? (
              <Group justify="center" gap="xs" py="xl" role="status">
                <Loader size="sm" />
                <Text size="sm" c="dimmed">
                  Loading history charts…
                </Text>
              </Group>
            ) : historyError ? (
              <Alert
                icon={<IconAlertTriangle size={16} stroke={1.8} />}
                color="red"
                variant="light"
                title="History charts unavailable"
              >
                <Stack gap="xs" align="flex-start">
                  <Text size="sm">
                    Per-model history could not be loaded. All-time statistics are unaffected.
                  </Text>
                  <Button
                    mih={44}
                    variant="light"
                    color="red"
                    onClick={onRetryHistory}
                    loading={historyFetching}
                  >
                    Retry
                  </Button>
                </Stack>
              </Alert>
            ) : (
              <>
                <DetailSection title="Performance history">
                  <HistoryLineChart
                    title="First byte"
                    description="Median time to first token"
                    data={historyData([series?.ttft_p50])}
                    series={[{ name: 'series0', label: 'First byte', formatter: historyFormatters.seconds }]}
                  />
                  <HistoryLineChart
                    title="Full response"
                    description="Median end-to-end latency"
                    data={historyData([series?.e2e_p50])}
                    series={[
                      { name: 'series0', label: 'Full response', formatter: historyFormatters.seconds },
                    ]}
                  />
                  <HistoryLineChart
                    title="Throughput"
                    description="Median output rate"
                    data={historyData([series?.throughput_p50])}
                    series={tpsSeries(historyFormatters)}
                  />
                </DetailSection>

                <DetailSection title="Traffic">
                  <HistoryBarChart
                    title="Requests"
                    description="Upstream calls per interval"
                    points={series?.requests ?? []}
                  />
                  <HistoryLineChart
                    title="Success rate"
                    description="Share of requests that succeeded"
                    data={historyData([series?.success_rate])}
                    series={[
                      { name: 'series0', label: 'Success rate', formatter: historyFormatters.percent },
                    ]}
                  />
                  <HistoryLineChart
                    title="Tool calls"
                    description="Tool calls issued per interval"
                    data={historyData([series?.tool_calls])}
                    series={[{ name: 'series0', label: 'Calls', formatter: historyFormatters.count }]}
                  />
                  <HistoryLineChart
                    title="Tool error rate"
                    description="Share of tool calls that errored"
                    data={historyData([series?.tool_errors])}
                    series={[{ name: 'series0', label: 'Errors', formatter: historyFormatters.percent }]}
                  />
                  <HistoryLineChart
                    title="Token volume"
                    description="Input and output tokens per interval"
                    data={historyData([series?.tokens_in, series?.tokens_out])}
                    series={tokenMixSeries(historyFormatters)}
                  />
                </DetailSection>
              </>
            )}
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}

function DetailStat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
      <Text size="xs" c="dimmed" fw={600} style={{ letterSpacing: '0.03em' }}>
        {label}
      </Text>
      <Text
        fz={22}
        fw={700}
        lh={1.15}
        mt={2}
        style={{ fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.02em' }}
      >
        {value}
      </Text>
      {hint && (
        <Text size="xs" c="dimmed" mt={1}>
          {hint}
        </Text>
      )}
    </Box>
  )
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Paper withBorder radius="lg" p="md" miw={0}>
      <Title order={3} size="h5" mb="md" style={{ overflowWrap: 'anywhere' }}>
        {title}
      </Title>
      <Stack gap="sm" miw={0}>
        {children}
      </Stack>
    </Paper>
  )
}
