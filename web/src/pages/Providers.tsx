import { useState, type ReactNode } from 'react'
import {
  Box,
  Badge,
  Button,
  Card,
  Code,
  Divider,
  Drawer,
  Group,
  Loader,
  Paper,
  RingProgress,
  ScrollArea,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Table,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconServerOff } from '@tabler/icons-react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { fetchBackendStatsSeries, fetchOverview, fetchStats } from '../api'
import type { ModelStat, OverviewBackend, StatsSeries } from '../api'
import { useMediaQuery } from '@mantine/hooks'
import { fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend } from '../components/TokenMixBar'
import {
  HistoryBarChart,
  HistoryLineChart,
  historyData,
  historyFormatters,
} from '../components/HistoryCharts'
import { providerSegments } from './Overview'
import { Fade } from '../App'

export default function ProvidersPage() {
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const pal = useChartPalette()

  const backends = ovQ.data?.backends ?? []
  const models = statsQ.data?.models ?? []
  const segByBackend = new Map(providerSegments(models, pal.series))
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const [selected, setSelected] = useState<OverviewBackend | null>(null)
  const [historyRange, setHistoryRange] = useState('24h')

  const selectedSeriesQ = useQuery({
    queryKey: ['stats-series', 'backend', selected?.name, historyRange],
    queryFn: () => fetchBackendStatsSeries(selected!.name, historyRange),
    enabled: !!selected,
    placeholderData: keepPreviousData,
  })

  return (
    <Fade fetching={ovQ.isFetching || statsQ.isFetching}>
      <Stack gap="md">
        <div>
          <Title order={4} mb={2}>Providers</Title>
          <Text size="xs" c="dimmed">
            {backends.length} configured · health, token mix, and catalog per provider
          </Text>
        </div>
        {ovQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : backends.length === 0 ? (
          <Stack align="center" py="xl" gap={6}>
            <ThemeIcon variant="light" color="gray" size="lg" radius="xl">
              <IconServerOff size={20} stroke={1.6} />
            </ThemeIcon>
            <Text fw={600}>No providers configured</Text>
            <Text size="sm" c="dimmed" ta="center" maw={340}>
              Add a backend to the proxy config and it will appear here.
            </Text>
          </Stack>
        ) : (
          <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
            {backends.map((b) => (
              <ProviderCard
                key={b.name}
                backend={b}
                routes={(ovQ.data?.routes ?? []).filter((r) => r.backend === b.name)}
                segments={segByBackend.get(b.name) ?? []}
                models={models.filter((m) => m.backend === b.name)}
                onInspect={() => setSelected(b)}
              />
            ))}
          </SimpleGrid>
        )}

        <Drawer
          opened={!!selected}
          onClose={() => setSelected(null)}
          position="right"
          size={isMobile ? '100%' : 'lg'}
          title={selected && (
            <Box style={{ minWidth: 0 }}>
              <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>Provider</Text>
              <Group gap="xs" wrap="nowrap">
                <Text fw={700} truncate>{selected.name}</Text>
              </Group>
            </Box>
          )}
        >
          {selected && (
            <ProviderDetail
              backend={selected}
              models={models.filter((m) => m.backend === selected.name)}
              series={selectedSeriesQ.data?.series}
              range={historyRange}
              onRangeChange={setHistoryRange}
            />
          )}
        </Drawer>
      </Stack>
    </Fade>
  )
}

// Colored dot + explicit label so key/catalog state is never color-alone;
// same status hues as UptimeBadge.
function StatusDot({
  ok,
  okLabel,
  badLabel,
}: {
  ok: boolean
  okLabel: string
  badLabel: string
}) {
  const color = ok ? '#0ca30c' : '#ec835a'
  return (
    <Group gap={5} wrap="nowrap">
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: '50%',
          background: color,
          display: 'inline-block',
          flexShrink: 0,
        }}
      />
      <Text size="xs" c="dimmed">
        {ok ? okLabel : badLabel}
      </Text>
    </Group>
  )
}

function CardSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Box>
      <Text
        size="xs"
        tt="uppercase"
        c="dimmed"
        fw={600}
        mb={6}
        style={{ letterSpacing: '0.03em' }}
      >
        {title}
      </Text>
      <Box style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>{children}</Box>
    </Box>
  )
}

function ProviderCard({
  backend: b,
  routes,
  segments,
  models,
  onInspect,
}: {
  backend: OverviewBackend
  routes: { model: string; backend: string; upstream: string }[]
  segments: Parameters<typeof TokenMixBar>[0]['segments']
  models: ModelStat[]
  onInspect: () => void
}) {
  const [catalogExpanded, setCatalogExpanded] = useState(false)
  const requests = models.reduce((s, m) => s + m.requests, 0)
  const successes = models.reduce((s, m) => s + m.successes, 0)
  const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
  const uptime = requests ? successes / requests : 0
  const segTotal = segments.reduce((s, x) => s + x.value, 0)
  const shownModels = catalogExpanded ? (b.models ?? []) : (b.models?.slice(0, 5) ?? [])
  const extra = (b.models?.length ?? 0) - shownModels.length

  // Ring color mirrors UptimeBadge thresholds; the badge under the ring
  // carries the icon+label so state is never color-alone.
  const ringColor = !requests ? 'gray' : uptime >= 0.99 ? 'teal' : uptime >= 0.9 ? 'yellow' : 'red'

  return (
    <Card
      withBorder
      radius="lg"
      p="lg"
      onClick={onInspect}
      style={{ cursor: 'pointer', height: '100%' }}
    >
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="md">
        {/* Left: identity + config health. */}
        <Box style={{ minWidth: 0 }}>
          <Group gap="xs" mb={4}>
            <Title order={5} mb={0}>
              {b.name}
            </Title>
            <Badge size="sm" variant="light" color={b.enabled ? 'teal' : 'gray'}>
              {b.enabled ? 'enabled' : 'disabled'}
            </Badge>
          </Group>
          <Code>{b.host}</Code>
          <Group gap="sm" wrap="wrap" mt={8}>
            {b.name === 'grok' || b.name === 'workbuddy' ? (
              <Group gap="xs">
                <StatusDot
                  ok={b.authConfigured}
                  okLabel={`${b.name === 'grok' ? 'xAI' : 'WorkBuddy'} account signed in`}
                  badLabel={`${b.name === 'grok' ? 'xAI' : 'WorkBuddy'} account not signed in`}
                />
                <Button component="a" href={b.name === 'grok' ? '/login' : '/login/workbuddy'} size="compact-xs" variant="light">
                  {b.authConfigured ? 'Sign in again' : 'Sign in'}
                </Button>
              </Group>
            ) : (
              <StatusDot
                ok={b.hasKey}
                okLabel="API key set"
                badLabel="API key missing"
              />
            )}
            <StatusDot
              ok={b.catalogOK}
              okLabel="catalog ok"
              badLabel="catalog unavailable"
            />
          </Group>
        </Box>
        {/* Right: uptime ring with its state badge stacked under it so the
              pair reads as one unit. */}
        <Stack align="center" gap={4} style={{ flexShrink: 0 }}>
          <Tooltip
            label={
              requests
                ? `${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`
                : 'No requests recorded yet'
            }
            withArrow
          >
            <RingProgress
              size={84}
              thickness={7}
              roundCaps
              sections={[{ value: requests ? uptime * 100 : 0, color: ringColor }]}
              label={
                <Text ta="center" size="xs" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
                  {requests ? fmtPct(uptime, 0) : '—'}
                </Text>
              }
              aria-label={`uptime ${fmtPct(uptime)}`}
            />
          </Tooltip>
          <UptimeBadge uptime={uptime} requests={requests} />
        </Stack>
      </Group>

      {/* Stats show whenever traffic exists — even if every request carried
            zero tokens (segments all empty), the counts still matter. */}
      {requests > 0 && (
        <>
          <Divider my="sm" />
          {segTotal > 0 && (
            <>
              <TokenMixBar segments={segments} height={14} />
              <TokenLegend segments={segments} showPercent />
            </>
          )}
          <Text size="xs" c="dimmed" mt={segTotal > 0 ? 6 : 0}>
            {fmtInt(requests)} requests · uptime {fmtPct(uptime)} · tools{' '}
            {fmtInt(toolCalls)} ({fmtPct(toolCalls ? toolErrors / toolCalls : 0)} err)
          </Text>
        </>
      )}

      {routes.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Routes · ${routes.length}`}>
            {routes.map((r) => (
              <Code key={r.model} style={{ fontSize: '0.72rem' }}>
                {r.model} → {r.upstream || '(as requested)'}
              </Code>
            ))}
          </CardSection>
        </>
      )}

      {shownModels.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Catalog · ${(b.models?.length ?? 0)}`}>
            {shownModels.map((m) => (
              <Group key={m} gap={4} wrap="nowrap">
                <Code style={{ fontSize: '0.72rem' }}>{m}</Code>
                {b.modelCredits?.[m] && (
                  <Badge size="xs" variant="light" color="violet">
                    {b.modelCredits[m]}
                  </Badge>
                )}
              </Group>
            ))}
            {extra > 0 && (
              <Button
                size="compact-xs"
                variant="subtle"
                aria-expanded={catalogExpanded}
                onClick={(event) => {
                  event.stopPropagation()
                  setCatalogExpanded(true)
                }}
              >
                Show all (+{extra})
              </Button>
            )}
            {catalogExpanded && (b.models?.length ?? 0) > 5 && (
              <Button
                size="compact-xs"
                variant="subtle"
                aria-expanded={catalogExpanded}
                onClick={(event) => {
                  event.stopPropagation()
                  setCatalogExpanded(false)
                }}
              >
                Show less
              </Button>
            )}
          </CardSection>
        </>
      )}
    </Card>
  )
}

function ProviderDetail({
  models,
  series,
  range,
  onRangeChange,
}: {
  backend: OverviewBackend
  models: ModelStat[]
  series?: StatsSeries
  range: string
  onRangeChange: (value: string) => void
}) {
  const requests = models.reduce((sum, model) => sum + model.requests, 0)
  const successes = models.reduce((sum, model) => sum + model.successes, 0)
  const toolCalls = models.reduce((sum, model) => sum + model.tool_calls, 0)
  const toolErrors = models.reduce((sum, model) => sum + model.tool_errors, 0)

  return (
    <ScrollArea style={{ height: 'calc(100vh - 90px)' }} type="auto">
      <Stack gap="lg" pr="sm">
        <Group justify="space-between" align="center" wrap="nowrap" gap="xs">
          <Text size="sm" fw={600}>History</Text>
          <SegmentedControl
            size="compact-xs"
            value={range}
            onChange={onRangeChange}
            data={[
              { value: '1h', label: '1h' },
              { value: '6h', label: '6h' },
              { value: '24h', label: '24h' },
              { value: '7d', label: '7d' },
            ]}
          />
        </Group>

        <SimpleGrid cols={2} spacing="md">
          <Paper withBorder radius="md" p="sm">
            <Text size="xs" c="dimmed">Requests</Text>
            <Text fz={24} fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtInt(requests)}</Text>
          </Paper>
          <Paper withBorder radius="md" p="sm">
            <Text size="xs" c="dimmed">Uptime</Text>
            <Text fz={24} fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtPct(requests ? successes / requests : 0)}</Text>
          </Paper>
        </SimpleGrid>

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
        <HistoryBarChart
          title="Requests"
          description="Requests per interval"
          points={series?.requests ?? []}
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
        <HistoryBarChart
          title="Tool calls"
          description="Observed calls per interval"
          points={series?.tool_calls ?? []}
        />

        <Divider my="xs" />
        <Title order={6}>Model performance</Title>
        <Table verticalSpacing="xs" horizontalSpacing="sm">
          <Table.Thead><Table.Tr><Table.Th>Model</Table.Th><Table.Th ta="right">Req</Table.Th><Table.Th ta="right">TTFT</Table.Th><Table.Th ta="right">E2E</Table.Th><Table.Th ta="right">tok/s</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {[...models].sort((a, b) => b.requests - a.requests).map((model) => (
              <Table.Tr key={`${model.backend}/${model.model}`}>
                <Table.Td><Code>{model.model}</Code></Table.Td>
                <Table.Td ta="right">{fmtInt(model.requests)}</Table.Td>
                <Table.Td ta="right">{fmtSec(model.ttft_seconds.p50)}</Table.Td>
                <Table.Td ta="right">{fmtSec(model.e2e_seconds.p50)}</Table.Td>
                <Table.Td ta="right">{fmtTps(model.throughput_tps.p50)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        {toolCalls > 0 && (
          <Text size="xs" c="dimmed">{fmtInt(toolCalls)} tool calls · {fmtPct(toolCalls ? toolErrors / toolCalls : 0)} errors</Text>
        )}
      </Stack>
    </ScrollArea>
  )
}
