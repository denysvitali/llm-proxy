import { useId, useState, type ReactNode } from 'react'
import {
  Alert,
  Box,
  Badge,
  Button,
  Card,
  Code,
  Divider,
  Drawer,
  Group,
  Indicator,
  Loader,
  Paper,
  RingProgress,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconLogin, IconServerOff } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchBackendStatsSeries, fetchGrokUsage, fetchOverview, fetchStats } from '../api'
import type { GrokUsage, ModelStat, OverviewBackend, StatsSeries } from '../api'
import { GrokUsageCompact } from '../components/GrokUsageCard'
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
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { TimeRangeControl } from '../components/TimeRangeControl'
import { providerSegments } from './Overview'
import { Fade } from '../App'

const CATALOG_PREVIEW = 6
const ROUTES_PREVIEW = 8

// fmtPct renders an exact zero as an em dash, which would hide a genuine
// 0% success rate (every request failed) or a 0% tool error rate. Render a
// real zero honestly; the truly-absent cases ('—') are guarded by callers.
function pct(ratio: number, digits = 1): string {
  if (ratio === 0) return `${(0).toFixed(digits)}%`
  return fmtPct(ratio, digits)
}

type StatsState = 'ready' | 'loading' | 'unavailable'
type HealthState = 'healthy' | 'degraded' | 'unhealthy' | 'no-traffic'

// Health is the observed request-success ratio over recorded traffic (same
// thresholds as UptimeBadge). Zero requests is "no traffic" — nothing wrong —
// never a verdict.
function healthState(requests: number, uptime: number): HealthState {
  if (requests <= 0) return 'no-traffic'
  return uptime >= 0.99 ? 'healthy' : uptime >= 0.9 ? 'degraded' : 'unhealthy'
}

// Aggregate one backend's ModelStat rows into the numbers its card, the
// drawer, and the page summary all share.
function backendAgg(models: ModelStat[], backendName: string) {
  const ms = models.filter((m) => m.backend === backendName)
  const requests = ms.reduce((s, m) => s + m.requests, 0)
  const successes = ms.reduce((s, m) => s + m.successes, 0)
  const toolCalls = ms.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = ms.reduce((s, m) => s + m.tool_errors, 0)
  return {
    requests,
    successes,
    uptime: requests ? successes / requests : 0,
    toolCalls,
    toolErrors,
  }
}

// Account-backed providers sign in through the proxy's web login flow; this
// table is the single source for label + login path in card and drawer.
// grok mounts at /login, the others at /login/<name>.
const ACCOUNT_AUTH: Record<string, { label: string; login: string }> = {
  grok: { label: 'xAI', login: '/login' },
  workbuddy: { label: 'WorkBuddy', login: '/login/workbuddy' },
  codex: { label: 'ChatGPT', login: '/login/codex' },
  zcode: { label: 'ZCode', login: '/login/zcode' },
}

export default function ProvidersPage() {
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const grokUsageEnabled = ovQ.data?.grokUsage.configured ?? false
  const grokUsageQ = useQuery({
    queryKey: ['grok-usage'],
    queryFn: fetchGrokUsage,
    enabled: grokUsageEnabled,
    refetchInterval: 60_000,
    retry: 1,
  })
  const pal = useChartPalette()

  const backends = ovQ.data?.backends ?? []
  // Stats availability drives every health signal: without it the page must
  // say "no data", never a green-looking "no traffic".
  const statsState: StatsState = statsQ.data ? 'ready' : statsQ.isPending ? 'loading' : 'unavailable'
  const models = statsQ.data?.models ?? []
  const segByBackend = new Map(providerSegments(models, pal.series))
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const [selectedName, setSelectedName] = useState<string | null>(null)
  // Resolve the drawer selection from current overview data, so a backend
  // removed from config while the drawer is open doesn't render a stale object.
  const selected = backends.find((b) => b.name === selectedName) ?? null
  const [historyRange, setHistoryRange] = useState('24h')

  const selectedSeriesQ = useQuery({
    queryKey: ['stats-series', 'backend', selected?.name, historyRange],
    queryFn: () => fetchBackendStatsSeries(selected!.name, historyRange),
    enabled: !!selected,
    // No placeholderData: never show another provider's or another range's
    // history under the current selection while it loads.
  })

  const enabledCount = backends.filter((b) => b.enabled).length
  const healthyCount = backends.filter((b) => {
    const agg = backendAgg(models, b.name)
    return healthState(agg.requests, agg.uptime) === 'healthy'
  }).length
  const missingAuthCount = backends.filter((b) => !b.hasKey && !b.authConfigured).length

  const selectedModels = selected ? models.filter((m) => m.backend === selected.name) : []
  const selectedAgg = backendAgg(models, selected?.name ?? '')

  return (
    <Fade pending={ovQ.isPending || statsQ.isPending}>
      <Stack gap="md">
        <PageHeader
          title="Providers"
          subtitle={`${backends.length} configured · health, token mix, and catalog`}
        />
        {ovQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : ovQ.isError ? (
          <Alert variant="light" color="red" title="Couldn't load providers">
            {ovQ.error.message}
            <Group gap={4} mt={6}>
              <Button size="xs" mih={44} variant="light" onClick={() => ovQ.refetch()}>
                Retry
              </Button>
            </Group>
          </Alert>
        ) : backends.length === 0 ? (
          <EmptyState
            icon={<IconServerOff size={20} stroke={1.6} />}
            title="No providers configured"
            hint="Add a backend to the proxy config and it will appear here."
          />
        ) : (
          <>
            {/* A failed or in-flight stats fetch must not read as "no traffic":
                say so explicitly above the cards. */}
            {statsState !== 'ready' && (
              <Alert
                variant="light"
                color="gray"
                title={statsState === 'loading' ? 'Loading request stats…' : 'Request stats unavailable'}
              >
                {statsState === 'loading'
                  ? 'Health and token mix appear once stats finish loading.'
                  : `Health and token mix are unavailable right now${statsQ.error?.message ? ` (${statsQ.error.message})` : ''}; provider configuration below is still current.`}
              </Alert>
            )}
            <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
              <CompactStat label="Configured" value={fmtInt(backends.length)} />
              <CompactStat label="Enabled" value={fmtInt(enabledCount)} />
              <CompactStat label="Healthy" value={statsState === 'ready' ? fmtInt(healthyCount) : '—'} />
              <CompactStat label="Missing auth" value={fmtInt(missingAuthCount)} />
            </SimpleGrid>
            <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
              {backends.map((b) => (
                <ProviderCard
                  key={b.name}
                  backend={b}
                  routes={(ovQ.data?.routes ?? []).filter((r) => r.backend === b.name)}
                  segments={segByBackend.get(b.name) ?? []}
                  models={models.filter((m) => m.backend === b.name)}
                  statsState={statsState}
                  grokUsage={b.name === 'grok' ? grokUsageQ.data : undefined}
                  grokUsageLoading={b.name === 'grok' && grokUsageQ.isPending}
                  grokUsageError={b.name === 'grok' ? grokUsageQ.error?.message : undefined}
                  onInspect={() => setSelectedName(b.name)}
                />
              ))}
            </SimpleGrid>
          </>
        )}

        <Drawer
          opened={!!selected}
          onClose={() => setSelectedName(null)}
          position="right"
          size={isMobile ? '100%' : 'lg'}
          title={selected && (
            <Box miw={0}>
              <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>Provider</Text>
              <Group gap="xs" wrap="nowrap">
                <Text fw={700} truncate>{selected.name}</Text>
                {statsState === 'ready' ? (
                  <UptimeBadge uptime={selectedAgg.uptime} requests={selectedAgg.requests} />
                ) : (
                  <UptimeBadge uptime={Number.NaN} requests={Number.NaN} />
                )}
              </Group>
            </Box>
          )}
        >
          {selected && (
            <ProviderDetail
              backend={selected}
              models={selectedModels}
              statsState={statsState}
              grokUsage={selected.name === 'grok' ? grokUsageQ.data : undefined}
              series={selectedSeriesQ.data?.series}
              seriesLoading={selectedSeriesQ.isPending}
              seriesError={selectedSeriesQ.error?.message}
              range={historyRange}
              onRangeChange={setHistoryRange}
            />
          )}
        </Drawer>
      </Stack>
    </Fade>
  )
}

function CompactStat({ label, value }: { label: string; value: string }) {
  const labelId = useId()
  return (
    <Paper withBorder radius="lg" p="md" miw={0} role="group" aria-labelledby={labelId}>
      <Text id={labelId} size="xs" tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '0.03em', overflowWrap: 'anywhere' }}>
        {label}
      </Text>
      <Text fz={22} fw={700} style={{ fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere' }}>
        {value}
      </Text>
    </Paper>
  )
}

// A native indicator plus explicit text keeps configuration state readable
// without color; theme roles match the rest of the health chrome.
function StatusDot({
  ok,
  okLabel,
  badLabel,
}: {
  ok: boolean
  okLabel: string
  badLabel: string
}) {
  return (
    <Group gap="xs" wrap="nowrap" miw={0}>
      <Indicator
        size={7}
        w={7}
        h={7}
        position="middle-center"
        color={ok ? 'teal' : 'yellow'}
        zIndex={0}
        aria-hidden="true"
        style={{ flexShrink: 0 }}
      />
      <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
        {ok ? okLabel : badLabel}
      </Text>
    </Group>
  )
}

// Auth state for one provider: account-backed backends show account sign-in
// state plus a link to the login flow; key-based ones show key presence.
function AuthStatus({ backend }: { backend: OverviewBackend }) {
  const account = ACCOUNT_AUTH[backend.name]
  if (account) {
    return (
      <Group gap="xs" miw={0} wrap="wrap">
        <StatusDot
          ok={backend.authConfigured}
          okLabel={`${account.label} account signed in`}
          badLabel={`${account.label} account not signed in`}
        />
        <Button
          component="a"
          href={account.login}
          size="xs"
          mih={44}
          color="violet"
          variant="light"
          onClick={(event) => event.stopPropagation()}
          leftSection={<IconLogin size={12} stroke={1.8} aria-hidden="true" />}
        >
          {backend.authConfigured ? 'Sign in again' : 'Sign in'}
        </Button>
      </Group>
    )
  }
  return <StatusDot ok={backend.hasKey} okLabel="API key set" badLabel="API key missing" />
}

function CardSection({ title, children }: { title: string; children: ReactNode }) {
  const titleId = useId()
  return (
    <Stack component="section" aria-labelledby={titleId} gap="xs" miw={0}>
      <Title id={titleId} order={5} mb={0} style={{ overflowWrap: 'anywhere' }}>
        {title}
      </Title>
      <Group gap={6} miw={0} align="flex-start" wrap="wrap">{children}</Group>
    </Stack>
  )
}

function ProviderCard({
  backend: b,
  routes,
  segments,
  models,
  statsState,
  grokUsage,
  grokUsageLoading,
  grokUsageError,
  onInspect,
}: {
  backend: OverviewBackend
  routes: { model: string; backend: string; upstream: string }[]
  segments: Parameters<typeof TokenMixBar>[0]['segments']
  models: ModelStat[]
  statsState: StatsState
  grokUsage?: GrokUsage
  grokUsageLoading?: boolean
  grokUsageError?: string
  onInspect: () => void
}) {
  const [catalogExpanded, setCatalogExpanded] = useState(false)
  const agg = backendAgg(models, b.name)
  const requests = agg.requests
  const uptime = agg.uptime
  const segTotal = segments.reduce((s, x) => s + x.value, 0)
  const shownModels = catalogExpanded ? (b.models ?? []) : (b.models?.slice(0, CATALOG_PREVIEW) ?? [])
  const extra = (b.models?.length ?? 0) - shownModels.length
  const shownRoutes = routes.slice(0, ROUTES_PREVIEW)
  const extraRoutes = routes.length - shownRoutes.length
  const statsReady = statsState === 'ready'

  // Ring color mirrors UptimeBadge thresholds; the badge under the ring
  // carries the icon+label so state is never color-alone. While stats are
  // unavailable the ring is a gray "—" and the badge reads "no data" instead
  // of the misleading "no traffic".
  const ringColor =
    !statsReady || !requests ? 'gray' : uptime >= 0.99 ? 'teal' : uptime >= 0.9 ? 'yellow' : 'red'
  const ringTooltip = !statsReady
    ? 'Request stats unavailable'
    : requests
      ? `${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`
      : 'No requests recorded yet'

  return (
    <Card
      withBorder
      radius="lg"
      p="md"
      onClick={onInspect}
      miw={0}
      h="100%"
      style={{ cursor: 'pointer' }}
    >
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="md">
        {/* Left: identity + config health. */}
        <Box miw={0}>
          <Group gap="xs" mb={4} wrap="wrap">
            <Title order={4} mb={0} style={{ overflowWrap: 'anywhere' }}>
              {b.name}
            </Title>
            <Badge size="sm" variant="light" color={b.enabled ? 'teal' : 'gray'}>
              {b.enabled ? 'enabled' : 'disabled'}
            </Badge>
            {/* Keyboard/AT path to the drawer; the card's own click affordance
                is pointer-only. */}
            <Button
              size="xs"
              mih={44}
              variant="subtle"
              aria-label={`Inspect ${b.name}`}
              onClick={(event) => {
                event.stopPropagation()
                onInspect()
              }}
            >
              Details
            </Button>
          </Group>
          <Code style={{ overflowWrap: 'anywhere' }}>{b.host}</Code>
          <Group gap="sm" wrap="wrap" mt={8}>
            <AuthStatus backend={b} />
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
          <Tooltip label={ringTooltip} withArrow events={{ hover: true, focus: true, touch: true }}>
            <RingProgress
              size={84}
              thickness={7}
              roundCaps
              sections={[{ value: statsReady && requests ? uptime * 100 : 0, color: ringColor }]}
              label={
                <Text ta="center" size="xs" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
                  {statsReady && requests ? pct(uptime, 0) : '—'}
                </Text>
              }
              role="img"
              tabIndex={0}
              aria-label={ringTooltip}
            />
          </Tooltip>
          {statsReady ? (
            <UptimeBadge uptime={uptime} requests={requests} />
          ) : (
            <UptimeBadge uptime={Number.NaN} requests={Number.NaN} />
          )}
        </Stack>
      </Group>

      {b.name === 'grok' && (grokUsage || grokUsageLoading || grokUsageError) && (
        <>
          <Divider my="sm" />
          {grokUsageLoading && !grokUsage ? (
            <Group justify="center" py={4}><Loader size="xs" /></Group>
          ) : grokUsageError && !grokUsage ? (
            <Text size="xs" c="dimmed">Usage unavailable</Text>
          ) : grokUsage ? (
            <GrokUsageCompact usage={grokUsage} />
          ) : null}
        </>
      )}

      {/* Stats show whenever traffic exists — even if every request carried
            zero tokens (segments all empty), the counts still matter. */}
      {statsReady && requests > 0 && (
        <>
          <Divider my="sm" />
          {segTotal > 0 && (
            <>
              <TokenMixBar segments={segments} height={14} />
              <TokenLegend segments={segments} showPercent />
            </>
          )}
          <Text size="xs" c="dimmed" mt={segTotal > 0 ? 6 : 0}>
            {fmtInt(requests)} requests · uptime {pct(uptime)} · tools{' '}
            {fmtInt(agg.toolCalls)} ({pct(agg.toolCalls ? agg.toolErrors / agg.toolCalls : 0)} err)
          </Text>
        </>
      )}

      {routes.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Routes · ${routes.length}`}>
            {shownRoutes.map((r) => (
              <Code key={r.model} fz="xs" miw={0} style={{ overflowWrap: 'anywhere' }}>
                {r.model} → {r.upstream || '(as requested)'}
              </Code>
            ))}
            {extraRoutes > 0 && (
              <Text size="xs" c="dimmed">+{extraRoutes} more</Text>
            )}
          </CardSection>
        </>
      )}

      {shownModels.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Catalog · ${(b.models?.length ?? 0)}`}>
            {shownModels.map((m) => (
              <Group key={m} gap={4} wrap="nowrap" miw={0} maw="100%">
                <Code fz="xs" miw={0} style={{ overflowWrap: 'anywhere' }}>{m}</Code>
                {b.modelCredits?.[m] && (
                  <Badge size="xs" variant="light" color="violet" style={{ flexShrink: 0 }}>
                    {b.modelCredits[m]}
                  </Badge>
                )}
              </Group>
            ))}
            {extra > 0 && (
              <Button
                size="xs" mih={44}
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
            {catalogExpanded && (b.models?.length ?? 0) > CATALOG_PREVIEW && (
              <Button
                size="xs" mih={44}
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
  backend,
  models,
  statsState,
  grokUsage,
  series,
  seriesLoading,
  seriesError,
  range,
  onRangeChange,
}: {
  backend: OverviewBackend
  models: ModelStat[]
  statsState: StatsState
  grokUsage?: GrokUsage
  series?: StatsSeries
  seriesLoading?: boolean
  seriesError?: string
  range: string
  onRangeChange: (value: string) => void
}) {
  const requests = models.reduce((sum, model) => sum + model.requests, 0)
  const successes = models.reduce((sum, model) => sum + model.successes, 0)
  const toolCalls = models.reduce((sum, model) => sum + model.tool_calls, 0)
  const toolErrors = models.reduce((sum, model) => sum + model.tool_errors, 0)
  const uptime = requests ? successes / requests : 0
  const statsReady = statsState === 'ready'

  // Health verdict only exists when stats are actually loaded; "no traffic"
  // (nothing wrong) is kept distinct from degraded/unhealthy traffic.
  const health = healthState(requests, uptime)
  const healthCopy = {
    'no-traffic': {
      title: 'No requests recorded yet',
      body: 'No traffic has reached this provider through the proxy yet. Its configuration and routes below still apply; send a request and health stats will appear here.',
      color: 'gray' as const,
    },
    degraded: {
      title: 'Degraded availability',
      body: `Some upstream requests to this provider are failing — ${pct(1 - uptime)} of ${requests.toLocaleString('en-US')} requests did not succeed. Check the per-model table below for the worst offenders.`,
      color: 'yellow' as const,
    },
    unhealthy: {
      title: 'Unhealthy',
      body: `A large share of upstream requests to this provider failed — ${pct(1 - uptime)} of ${requests.toLocaleString('en-US')}. Recent failures are listed on the Overview page.`,
      color: 'red' as const,
    },
  }

  return (
    <Box miw={0}>
      <Stack gap="lg" miw={0} pb="md">
        {statsReady && health !== 'healthy' && (
          <Alert
            variant="light"
            color={healthCopy[health].color}
            title={healthCopy[health].title}
          >
            {healthCopy[health].body}
          </Alert>
        )}

        {statsState !== 'ready' && (
          <Alert variant="light" color="gray" title="Per-model stats unavailable">
            {statsState === 'loading'
              ? 'Loading model stats…'
              : 'Per-model stats are temporarily unavailable; the configuration summary below is still current.'}
          </Alert>
        )}

        {seriesError && (
          <Alert variant="light" color="gray" title="History unavailable">
            {seriesError}
          </Alert>
        )}

        {/* Configuration summary: the drawer stays useful for identity/auth
            even when stats or history are unavailable. */}
        <CardSection title="Configuration">
          <Group gap="sm" wrap="wrap">
            <Code style={{ overflowWrap: 'anywhere' }}>{backend.host}</Code>
            <Badge size="sm" variant="light" color={backend.enabled ? 'teal' : 'gray'}>
              {backend.enabled ? 'enabled' : 'disabled'}
            </Badge>
            <AuthStatus backend={backend} />
            <StatusDot
              ok={backend.catalogOK}
              okLabel="catalog ok"
              badLabel="catalog unavailable"
            />
          </Group>
        </CardSection>

        <Group justify="space-between" align="center" wrap="wrap" gap="xs">
          <Title order={5}>History</Title>
          <TimeRangeControl value={range} onChange={onRangeChange} />
        </Group>

        {backend.name === 'grok' && grokUsage && <GrokUsageCompact usage={grokUsage} />}

        <Text size="xs" c="dimmed">All recorded traffic · totals are independent of the history range</Text>
        <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
          <CompactStat label="Requests" value={statsReady ? fmtInt(requests) : '—'} />
          <CompactStat label="Uptime" value={statsReady && requests ? pct(uptime) : '—'} />
          <CompactStat label="Models" value={statsReady ? fmtInt(models.length) : '—'} />
          <CompactStat
            label="Tool err rate"
            value={statsReady && toolCalls ? pct(toolErrors / toolCalls) : '—'}
          />
        </SimpleGrid>

        {seriesLoading && !series ? (
          <Group justify="center" py="xl"><Loader size="sm" /></Group>
        ) : seriesError && !series ? (
          <Text size="sm" c="dimmed">
            History charts are unavailable for this provider right now. The cumulative
            totals above still reflect all recorded traffic.
          </Text>
        ) : (
          <>
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
          </>
        )}

        <Divider my="xs" />
        <Title order={5}>Model performance</Title>
        {statsState === 'loading' ? (
          <Text size="sm" c="dimmed" role="status">Loading model stats…</Text>
        ) : statsState === 'unavailable' ? (
          <Text size="sm" c="dimmed">
            Model stats are temporarily unavailable.
          </Text>
        ) : models.length === 0 ? (
          <Text size="sm" c="dimmed">
            No requests recorded for this provider yet. Once traffic flows through the
            proxy, per-model uptime and latency appear here.
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={520}>
            <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="sm" style={{ fontVariantNumeric: 'tabular-nums' }}>
              <Table.Caption>Per-model performance · all recorded traffic</Table.Caption>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Model</Table.Th>
                  <Table.Th ta="right">Req</Table.Th>
                  <Table.Th>Uptime</Table.Th>
                  <Table.Th ta="right">TTFT</Table.Th>
                  <Table.Th ta="right">E2E</Table.Th>
                  <Table.Th ta="right">tok/s</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {[...models].sort((a, b) => b.requests - a.requests).map((model) => (
                  <Table.Tr key={`${model.backend}/${model.model}`}>
                    <Table.Td><Code style={{ overflowWrap: 'anywhere' }}>{model.model}</Code></Table.Td>
                    <Table.Td ta="right">{fmtInt(model.requests)}</Table.Td>
                    <Table.Td>
                      <UptimeBadge uptime={model.uptime} requests={model.requests} />
                    </Table.Td>
                    <Table.Td ta="right">{fmtSec(model.ttft_seconds.p50)}</Table.Td>
                    <Table.Td ta="right">{fmtSec(model.e2e_seconds.p50)}</Table.Td>
                    <Table.Td ta="right">{fmtTps(model.throughput_tps.p50)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {statsReady && toolCalls > 0 && (
          <Text size="xs" c="dimmed">
            {fmtInt(toolCalls)} tool calls · {pct(toolCalls ? toolErrors / toolCalls : 0)} errors
          </Text>
        )}
      </Stack>
    </Box>
  )
}
