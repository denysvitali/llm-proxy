import { useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import {
  ActionIcon,
  Alert,
  Box,
  Button,
  Card,
  Code,
  Drawer,
  Group,
  Loader,
  Paper,
  Progress,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import {
  IconActivity,
  IconAlertTriangle,
  IconArrowDown,
  IconArrowUp,
  IconArrowsSort,
  IconClock,
  IconCube,
  IconChevronRight,
  IconInboxOff,
  IconSearch,
  IconSearchOff,
  IconX,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchBackendStatsSeries, fetchStats } from '../api'
import type { ModelStat, StatsSeries } from '../api'
import { clampRate, fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import StatTile from '../components/StatTile'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { TimeRangeControl } from '../components/TimeRangeControl'
import UptimeBadge from '../components/UptimeBadge'
import StatusChips from '../components/StatusChips'
import PercentileBars from '../components/PercentileBars'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import {
  HistoryBarChart,
  HistoryLineChart,
  historyData,
  historyFormatters,
} from '../components/HistoryCharts'
import { Fade } from '../App'

type SortKey =
  | 'model'
  | 'requests'
  | 'uptime'
  | 'ttft'
  | 'e2e'
  | 'tps'
  | 'cache'
  | 'tools'
  | 'toolErr'

const columns: { key: SortKey; label: string; numeric?: boolean }[] = [
  { key: 'model', label: 'Backend / model' },
  { key: 'requests', label: 'Requests', numeric: true },
  { key: 'uptime', label: 'Uptime' },
  { key: 'ttft', label: 'TTFT p50/p90/p99', numeric: true },
  { key: 'e2e', label: 'E2E p50/p90/p99', numeric: true },
  { key: 'tps', label: 'tok/s p50', numeric: true },
  { key: 'cache', label: 'Cache hit', numeric: true },
  { key: 'tools', label: 'Tool calls', numeric: true },
  { key: 'toolErr', label: 'Tool err', numeric: true },
]

const sortOptions = [
  { value: 'requests', label: 'Requests' },
  { value: 'uptime', label: 'Uptime' },
  { value: 'ttft', label: 'TTFT (p50)' },
  { value: 'e2e', label: 'E2E latency (p50)' },
  { value: 'tps', label: 'Throughput (p50)' },
  { value: 'cache', label: 'Cache hit' },
  { value: 'tools', label: 'Tool calls' },
  { value: 'toolErr', label: 'Tool error rate' },
  { value: 'model', label: 'Name' },
]

// Token-kind segments in fixed categorical slot order; every chart on this
// page (cards, drawer) draws the same kind in the same color.
function mixSegments(m: ModelStat, colors: string[]): MixSegment[] {
  return [
    { name: 'input', color: colors[0], value: m.input_tokens },
    { name: 'output', color: colors[1], value: m.output_tokens },
    { name: 'cache read', color: colors[2], value: m.cache_read_tokens },
    { name: 'cache write', color: colors[3], value: m.cache_write_tokens },
  ]
}

// A recorded zero rate is different from a rate with no observations.
function observedRate(rate: number, observations: number): string {
  return observations > 0 && Number.isFinite(rate) ? `${(clampRate(rate) * 100).toFixed(1)}%` : '—'
}

// p90/p99 as a hover title behind the p50 cell; the drawer shows the same
// percentiles as full bars for keyboard/touch users.
function percentileTitle(v: { p50: number; p90: number; p99: number }, fmt: (n: number) => string): string {
  return `p90 ${fmt(v.p90)} · p99 ${fmt(v.p99)}`
}

function sortValue(m: ModelStat, key: SortKey): string | number {
  switch (key) {
    case 'model':
      return `${m.backend}/${m.model}`
    case 'requests':
      return m.requests
    case 'uptime':
      return m.uptime
    case 'ttft':
      return m.ttft_seconds.p50
    case 'e2e':
      return m.e2e_seconds.p50
    case 'tps':
      return m.throughput_tps.p50
    case 'cache':
      return m.cache_rate
    case 'tools':
      return m.tool_calls
    case 'toolErr':
      return m.tool_error_rate
  }
}

export default function ModelsPage() {
  const q = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  // Memoized so the rows useMemo below sees a stable identity between fetches.
  const models = useMemo(() => q.data?.models ?? [], [q.data])
  const pal = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  const [filter, setFilter] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  function clearFilter() {
    setFilter('')
    searchRef.current?.focus()
  }
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: 'requests', dir: -1 })
  const [selected, setSelected] = useState<ModelStat | null>(null)
  const [historyRange, setHistoryRange] = useState('24h')

  const selectedSeriesQ = useQuery({
    queryKey: ['stats-series', 'model', selected?.backend, selected?.model, historyRange],
    queryFn: () => fetchBackendStatsSeries(selected!.backend, historyRange, selected!.model),
    enabled: !!selected,
  })

  const rows = useMemo(() => {
    const f = filter.trim().toLowerCase()
    return models
      .filter((m) => !f || `${m.backend}/${m.model}`.toLowerCase().includes(f))
      .sort((a, b) => {
        const va = sortValue(a, sort.key)
        const vb = sortValue(b, sort.key)
        if (typeof va === 'string' || typeof vb === 'string')
          return String(va).localeCompare(String(vb)) * sort.dir
        return (va - vb) * sort.dir
      })
  }, [models, filter, sort])

  const summary = useMemo(() => {
    const requests = models.reduce((s, m) => s + m.requests, 0)
    const ttfts = models.map((m) => m.ttft_seconds.p50).filter((v) => Number.isFinite(v) && v > 0).sort((a, b) => a - b)
    const middle = Math.floor(ttfts.length / 2)
    const medianTtft = ttfts.length === 0 ? NaN : ttfts.length % 2 === 1
      ? ttfts[middle]
      : (ttfts[middle - 1] + ttfts[middle]) / 2
    const withErrors = models.filter(
      (m) => m.requests - m.successes > 0 || m.tool_errors > 0,
    ).length
    return { requests, medianTtft, withErrors }
  }, [models])

  function toggleSort(key: SortKey) {
    setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: key === 'model' ? 1 : -1 }))
  }

  return (
    <Fade pending={q.isPending}>
      <Stack gap="sm" className="models-page">
        <PageHeader
          title="Models"
          subtitle="Recorded model traffic, not the full provider catalog. Open a model for history and percentiles."
        />

        {models.length > 0 && (
          <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
            <StatTile
              label="Models tracked"
              value={fmtInt(models.length)}
              hint="per-model rows in this table"
              icon={<IconCube size={16} />}
              accent="brand"
            />
            <StatTile
              label="Requests"
              value={fmtInt(summary.requests)}
              hint="all recorded model traffic"
              icon={<IconActivity size={16} />}
              accent="brand"
            />
            <StatTile
              label="Median TTFT p50"
              value={fmtSec(summary.medianTtft)}
              hint="p50 across models with data"
              icon={<IconClock size={16} />}
              accent="teal"
            />
            <StatTile
              label="Models with errors"
              value={fmtInt(summary.withErrors)}
              hint="all time · tool errors count"
              icon={<IconAlertTriangle size={16} />}
              accent={summary.withErrors > 0 ? 'red' : 'gray'}
            />
          </SimpleGrid>
        )}

        <Stack gap="xs">
          {/* Live region announces result-count changes to screen readers as
              the filter narrows the list. */}
          <Text size="xs" c="dimmed" aria-live="polite" aria-atomic="true" style={{ overflowWrap: 'anywhere' }}>
            {q.isPending ? 'Loading tracked models…' : q.isError ? 'Model statistics unavailable.' : (
              `${filter.trim() ? `${rows.length} of ${models.length} match “${filter.trim()}”` : `Showing all ${models.length} tracked models`}, sorted by ${sortOptions.find((o) => o.value === sort.key)?.label.toLowerCase()} (${sort.dir === 1 ? 'ascending' : 'descending'})`
            )}
          </Text>
          <TextInput
            ref={searchRef}
            leftSection={<IconSearch size={14} />}
            rightSection={filter ? <CloseSearchButton onClear={clearFilter} /> : undefined}
            rightSectionWidth={44}
            styles={{ input: { minHeight: 44 } }}
            placeholder="Filter backend or model…"
            aria-label="Filter backend or model"
            value={filter}
            onChange={(e) => setFilter(e.currentTarget.value)}
          />
          {isMobile && models.length > 0 && (
            <Group gap="xs" wrap="nowrap">
              <Select
                leftSection={<IconArrowsSort size={14} />}
                data={sortOptions}
                value={sort.key}
                onChange={(v) => v && setSort({ key: v as SortKey, dir: v === 'model' ? 1 : -1 })}
                aria-label="Sort models by"
                allowDeselect={false}
                flex={1}
                size="sm"
                styles={{ input: { minHeight: 44 } }}
              />
              <Button
                variant="default"
                h={44}
                px="sm"
                style={{ flexShrink: 0 }}
                leftSection={sort.dir === 1 ? <IconArrowUp size={16} aria-hidden="true" /> : <IconArrowDown size={16} aria-hidden="true" />}
                aria-label={`Sort direction: ${sort.dir === 1 ? 'ascending' : 'descending'}. Switch to ${sort.dir === 1 ? 'descending' : 'ascending'}.`}
                onClick={() => setSort((s) => ({ ...s, dir: s.dir === 1 ? -1 : 1 }))}
              >
                {sort.dir === 1 ? 'Asc' : 'Desc'}
              </Button>
            </Group>
          )}
        </Stack>

        {q.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : q.isError ? (
          <Alert
            icon={<IconAlertTriangle size={16} stroke={1.8} />}
            color="red"
            variant="light"
            title="Couldn't load model stats"
          >
            <Stack gap="xs" align="flex-start">
              <Text size="sm">
                Model statistics are temporarily unavailable. {q.error.message}
              </Text>
              <Button size="compact-sm" variant="light" color="red" onClick={() => q.refetch()}>
                Retry
              </Button>
            </Stack>
          </Alert>
        ) : rows.length === 0 ? (
          <Stack gap={0} align="center">
            <EmptyState
              icon={
                models.length === 0 ? (
                  <IconInboxOff size={20} stroke={1.6} />
                ) : (
                  <IconSearchOff size={20} stroke={1.6} />
                )
              }
              title={models.length === 0 ? 'No model traffic yet' : 'No models match that filter'}
              hint={
                models.length === 0
                  ? 'Send a request through the proxy and per-model stats will land here.'
                  : 'Try a shorter fragment of the backend or model name.'
              }
            />
            {filter.trim() && models.length > 0 && (
              <Button variant="light" mih={44} onClick={clearFilter}>
                Clear filter
              </Button>
            )}
          </Stack>
        ) : isMobile ? (
          <SimpleGrid cols={1} spacing="sm">
            {rows.map((m) => (
              <ModelCard
                key={`${m.backend}/${m.model}`}
                stat={m}
                colors={pal.series}
                onClick={() => setSelected(m)}
              />
            ))}
          </SimpleGrid>
        ) : (
          <ScrollArea>
            {/* Striped + highlight-on-hover keeps wide rows scannable; the
                  cursor signals the row opens the detail drawer. */}
            <Table verticalSpacing="xs" horizontalSpacing="md" highlightOnHover striped>
              <Table.Thead
                style={{
                  position: 'sticky',
                  top: 0,
                  background: 'var(--mantine-color-body)',
                  zIndex: 1,
                }}
              >
                <Table.Tr>
                  {columns.map((c) => (
                    <Table.Th
                      key={c.key}
                      scope="col"
                      ta={c.numeric ? 'right' : undefined}
                      aria-sort={sort.key === c.key ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'}
                    >
                      <UnstyledButton mih={44} onClick={() => toggleSort(c.key)} aria-label={`Sort by ${c.label}`}>
                        <Group gap={4} wrap="nowrap" justify={c.numeric ? 'flex-end' : 'flex-start'}>
                          <Text size="xs" fw={600} c="dimmed">
                            {c.label}
                            {sort.key === c.key && (sort.dir === 1 ? ' ↑' : ' ↓')}
                          </Text>
                        </Group>
                      </UnstyledButton>
                    </Table.Th>
                  ))}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((m) => (
                  <Table.Tr
                    key={`${m.backend}/${m.model}`}
                    onClick={() => setSelected(m)}
                    style={{ cursor: 'pointer' }}
                  >
                    <Table.Td>
                      {/* Backend as a muted eyebrow above the model name —
                            the model is what you scan for. */}
                      <UnstyledButton
                        mih={44}
                        aria-label={`Open details for ${m.backend} ${m.model}`}
                        onClick={(event) => {
                          event.stopPropagation()
                          setSelected(m)
                        }}
                        style={{ minWidth: 0 }}
                      >
                        <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>
                          {m.backend}
                        </Text>
                        <Code style={{ overflowWrap: 'anywhere' }}>{m.model}</Code>
                      </UnstyledButton>
                    </Table.Td>
                    <Num td={fmtInt(m.requests)} />
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        <UptimeBadge uptime={m.uptime} requests={m.requests} />
                        {Object.keys(m.status_codes ?? {}).length > 0 && (
                          <StatusChips codes={m.status_codes} limit={3} />
                        )}
                      </Group>
                    </Table.Td>
                    <Num td={fmtSec(m.ttft_seconds.p50)} title={percentileTitle(m.ttft_seconds, fmtSec)} />
                    <Num td={fmtSec(m.e2e_seconds.p50)} title={percentileTitle(m.e2e_seconds, fmtSec)} />
                    <Num td={fmtTps(m.throughput_tps.p50)} title={percentileTitle(m.throughput_tps, fmtTps)} />
                    <Num td={fmtPct(m.cache_rate)} />
                    <Num td={fmtInt(m.tool_calls)} />
                    <Num td={observedRate(m.tool_error_rate, m.tool_calls)} title={`${fmtInt(m.tool_errors)} errored · ${fmtInt(m.tool_calls)} calls`} />
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        )}
      </Stack>

      <Drawer
        opened={!!selected}
        onClose={() => setSelected(null)}
        position="right"
        size={isMobile ? '100%' : 'lg'}
        styles={{ title: { minWidth: 0, flex: 1 }, close: { flexShrink: 0 } }}
        closeButtonProps={{ 'aria-label': 'Close model details', size: 44 }}
        title={
          selected && (
            <Box style={{ minWidth: 0 }}>
              <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>
                {selected.backend}
              </Text>
              <Stack gap="xs">
                <Text fw={700} style={{ overflowWrap: 'anywhere' }}>
                  {selected.model}
                </Text>
                <UptimeBadge uptime={selected.uptime} requests={selected.requests} />
              </Stack>
            </Box>
          )
        }
      >
        {selected && (
          <ModelDetail
            stat={selected}
            colors={pal.series}
            series={selectedSeriesQ.data?.series}
            historyPending={selectedSeriesQ.isPending}
            historyError={selectedSeriesQ.isError}
            historyFetching={selectedSeriesQ.isFetching}
            onRetryHistory={() => selectedSeriesQ.refetch({ cancelRefetch: false })}
            range={historyRange}
            onRangeChange={setHistoryRange}
          />
        )}
      </Drawer>
    </Fade>
  )
}

function CloseSearchButton({ onClear }: { onClear: () => void }) {
  return (
    <ActionIcon aria-label="Clear filter" variant="subtle" color="gray" onClick={onClear} size={44}>
      <IconX size={14} stroke={1.8} />
    </ActionIcon>
  )
}

function ModelCard({
  stat: m,
  colors,
  onClick,
}: {
  stat: ModelStat
  colors: string[]
  onClick: () => void
}) {
  const tokTotal =
    m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_tokens
  const segs = mixSegments(m, colors)
  return (
    <Card
      withBorder
      radius="lg"
      p="md"
      data-model-card
      onClick={onClick}
      style={{ cursor: 'pointer' }}
    >
      <Group justify="space-between" align="flex-start" gap="xs" mb={12}>
        <Box w="100%" style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" fw={600}>
            {m.backend}
          </Text>
          {/* Wrap, don't truncate: long model IDs are the identity, and clipping
              them makes two cards indistinguishable. */}
          <Text fw={600} lh={1.3} style={{ overflowWrap: 'anywhere' }}>
            {m.model}
          </Text>
        </Box>
        <Group w="100%" justify="space-between" gap={4} wrap="nowrap">
          <UptimeBadge uptime={m.uptime} requests={m.requests} />
          {/* Explicit keyboard/touch affordance for the details the whole card
              also opens; the card tap stays for convenience. */}
          <ActionIcon
            aria-label={`Open details for ${m.backend} ${m.model}`}
            variant="subtle"
            color="gray"
            onClick={(e) => {
              e.stopPropagation()
              onClick()
            }}
            h={44}
            w={44}
          >
            <IconChevronRight size={18} stroke={1.8} />
          </ActionIcon>
        </Group>
      </Group>
      {/* Short labels: the drawer owns the verbose names; the card is a glance
            surface. Latency pair kept adjacent (TTFT then E2E). */}
      <SimpleGrid cols={3} spacing="sm">
        <Metric label="Requests" value={fmtInt(m.requests)} />
        <Metric label="TTFT" value={fmtSec(m.ttft_seconds.p50)} />
        <Metric label="E2E" value={fmtSec(m.e2e_seconds.p50)} />
        <Metric label="tok/s" value={fmtTps(m.throughput_tps.p50)} />
        <Metric label="Cache" value={fmtPct(m.cache_rate)} />
        <Metric label="Tool err" value={observedRate(m.tool_error_rate, m.tool_calls)} />
      </SimpleGrid>
      {/* Slim token-mix strip with its own legend: cache share is visible at a
            glance and the strip is explained rather than silent. Hidden until
            tokens exist to avoid noise. */}
      {tokTotal > 0 && (
        <Box mt={10}>
          <Text size="xs" c="dimmed" fw={600} mb={4}>
            Token mix
          </Text>
          <TokenMixBar segments={segs} height={8} />
          <TokenLegend segments={segs} compact />
        </Box>
      )}
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {value}
      </Text>
    </Box>
  )
}

function Num({ td, title }: { td: string | number; title?: string }) {
  return (
    <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }} title={title}>
      {td}
    </Table.Td>
  )
}

function ModelDetail({
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
  const latMax = Math.max(stat.ttft_seconds.p99, stat.e2e_seconds.p99)
  const successRate = stat.requests > 0 && Number.isFinite(stat.successes) ? clampRate(stat.successes / stat.requests) : NaN

  return (
    <Stack gap="lg">
      <DetailSection title="All recorded traffic">
        <SimpleGrid cols={2} spacing="md">
          <DetailStat label="Requests" value={fmtInt(stat.requests)} hint={`${observedRate(successRate, stat.requests)} succeeded`} />
          <DetailStat label="Latency p50" value={fmtSec(stat.e2e_seconds.p50)} hint={`TTFT ${fmtSec(stat.ttft_seconds.p50)}`} />
          <DetailStat label="Throughput" value={fmtTps(stat.throughput_tps.p50)} hint={`p90 ${fmtTps(stat.throughput_tps.p90)}`} />
          <DetailStat label="Tool error rate" value={observedRate(stat.tool_error_rate, stat.tool_calls)} hint={`${fmtInt(stat.tool_errors)} errored · ${fmtInt(stat.tool_calls)} calls`} />
        </SimpleGrid>
      </DetailSection>
      <Group justify="space-between" align="center" wrap="wrap" gap="sm">
        <Text size="xs" tt="uppercase" fw={700} c="dimmed" style={{ letterSpacing: '0.04em' }}>
          History range
        </Text>
        <TimeRangeControl value={range} onChange={onRangeChange} disabled={historyPending} />
      </Group>
      <Text size="xs" c="dimmed">
        The range applies to history charts only. Summary stats and percentile bars use all recorded traffic.
      </Text>

      {historyPending ? (
        <Group justify="center" gap="xs" py="xl" role="status">
          <Loader size="sm" />
          <Text size="sm" c="dimmed">Loading history charts…</Text>
        </Group>
      ) : historyError ? (
        <Alert
          icon={<IconAlertTriangle size={16} stroke={1.8} />}
          color="red"
          variant="light"
          title="History charts unavailable"
        >
          <Stack gap="xs" align="flex-start">
            <Text size="sm">Per-model history could not be loaded. All-time statistics are unaffected.</Text>
            <Button mih={44} variant="light" color="red" onClick={onRetryHistory} loading={historyFetching}>
              Retry
            </Button>
          </Stack>
        </Alert>
      ) : (
        <>
          <DetailSection title="Performance history">
            <HistoryLineChart
              title="Latency"
              description="Median first byte and full response"
              data={historyData([series?.ttft_p50, series?.e2e_p50])}
              series={[
                { name: 'series0', label: 'First byte', formatter: historyFormatters.seconds },
                { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds },
              ]}
            />
            <HistoryLineChart
              title="Throughput"
              description="Median output rate"
              data={historyData([series?.throughput_p50])}
              series={[{ name: 'series0', label: 'Tokens/sec', formatter: historyFormatters.tps }]}
            />
          </DetailSection>

          <DetailSection title="Traffic">
            <HistoryBarChart
              title="Requests"
              description="Upstream calls per interval"
              points={series?.requests ?? []}
            />
            <HistoryLineChart
              title="Tool calls vs errors"
              description="Calls issued and errored results, per interval"
              data={historyData([series?.tool_calls, series?.tool_errors])}
              series={[
                { name: 'series0', label: 'Calls', formatter: historyFormatters.count },
                { name: 'series1', label: 'Errors', formatter: historyFormatters.count },
              ]}
            />
            <HistoryLineChart
              title="Token volume"
              description="Input and output tokens per interval"
              data={historyData([series?.tokens_in, series?.tokens_out])}
              series={[
                { name: 'series0', label: 'Input', formatter: historyFormatters.count },
                { name: 'series1', label: 'Output', formatter: historyFormatters.count },
              ]}
            />
          </DetailSection>
        </>
      )}

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
        title={`Tokens · ${fmtInt(totalTok)} total · cache hit ${fmtPct(stat.cache_rate)}`}
      >
        <TokenMixBar segments={segs} height={20} />
        <TokenLegend segments={segs} showPercent />
      </DetailSection>
      <DetailSection title="Reliability">
        <DetailStat label="Success rate" value={observedRate(successRate, stat.requests)} />
        {Number.isFinite(successRate) ? (
          <Progress aria-label="Request success rate" value={successRate * 100} radius="sm" size="sm" color={successRate >= 0.99 ? 'teal' : successRate >= 0.9 ? 'yellow' : 'red'} />
        ) : <Text size="sm" c="dimmed">No request success observations yet.</Text>}
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
  )
}

function DetailStat({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint?: string
}) {
  return (
    <Box miw={0} style={{ overflowWrap: 'anywhere' }}>
      <Text size="xs" c="dimmed" fw={600} style={{ letterSpacing: '0.03em' }}>{label}</Text>
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
        <Text size="xs" c="dimmed" mt={1}>{hint}</Text>
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
      <Stack gap="sm" miw={0}>{children}</Stack>
    </Paper>
  )
}
