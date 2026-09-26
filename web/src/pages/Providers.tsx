import { useId, useMemo, useState, type ReactNode } from 'react'
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
  Select,
  TextInput,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import { IconLogin, IconServerOff, IconSearch, IconSearchOff, IconChevronRight } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchBackendStatsSeries, fetchGrokUsage, fetchOverview, fetchStats } from '../api'
import type { GrokUsage, ModelStat, OverviewBackend, StatsSeries } from '../api'
import GrokUsageCompact from '../components/GrokUsageCompact'
import { useMediaQuery } from '@mantine/hooks'
import { fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import StatTile from '../components/StatTile'
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
import { providerSegments, healthState } from '../lib/stats'
import { Fade } from '../App'

const CATALOG_PREVIEW = 3
const ROUTES_PREVIEW = 3

// fmtPct renders an exact zero as an em dash, which would hide a genuine
// 0% success rate (every request failed) or a 0% tool error rate. Render a
// real zero honestly; the truly-absent cases ('—') are guarded by callers.
function pct(ratio: number, digits = 1): string {
  if (ratio === 0) return `${(0).toFixed(digits)}%`
  return fmtPct(ratio, digits)
}

type StatsState = 'ready' | 'loading' | 'unavailable'

// Aggregate one backend's ModelStat rows into the numbers its card, the
// drawer, and the page summary all share.
function backendAgg(models: ModelStat[], backendName: string) {
  const ms = models.filter((m) => m.backend === backendName)
  const requests = ms.reduce((s, m) => s + m.requests, 0)
  const successes = ms.reduce((s, m) => s + m.successes, 0)
  const toolCalls = ms.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = ms.reduce((s, m) => s + m.tool_errors, 0)
  const statusCodes: Record<string, number> = {}
  for (const m of ms) {
    for (const [code, n] of Object.entries(m.status_codes ?? {})) {
      statusCodes[code] = (statusCodes[code] ?? 0) + n
    }
  }
  return {
    requests,
    successes,
    uptime: requests ? successes / requests : 0,
    toolCalls,
    toolErrors,
    statusCodes,
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
  const palSeries = pal.series
  const segByBackend = useMemo(() => new Map(providerSegments(models, palSeries)), [models, palSeries])
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const [search, setSearch] = useState('')
  const [healthFilter, setHealthFilter] = useState('all')
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

  const { enabledCount, healthyCount, missingAuthCount, needsAttentionCount } = useMemo(() => {
    let enabled = 0
    let healthy = 0
    let missingAuth = 0
    let attention = 0
    for (const b of backends) {
      if (b.enabled) enabled++
      const agg = backendAgg(models, b.name)
      const health = healthState(agg.requests, agg.uptime)
      if (health === 'healthy') healthy++
      if (!b.hasKey && !b.authConfigured) missingAuth++
      if (!b.catalogOK || (!b.hasKey && !b.authConfigured) || health === 'degraded' || health === 'unhealthy') attention++
    }
    return { enabledCount: enabled, healthyCount: healthy, missingAuthCount: missingAuth, needsAttentionCount: attention }
    // backends/models are new ?? [] arrays every render
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [backends, models])

  const visibleBackends = useMemo(() => {
    const query = search.trim().toLowerCase()
    return backends.filter((backend) => {
      const matchesSearch = !query || [backend.name, backend.host, ...(backend.models ?? [])]
        .some((value) => value.toLowerCase().includes(query))
      const agg = backendAgg(models, backend.name)
      const health = healthState(agg.requests, agg.uptime)
      const attention = !backend.catalogOK || (!backend.hasKey && !backend.authConfigured)
        || health === 'degraded' || health === 'unhealthy'
      return matchesSearch && (healthFilter === 'all' || (healthFilter === 'attention' ? attention : health === healthFilter))
    })
    // backends/models are new ?? [] arrays every render
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [backends, models, search, healthFilter])

  const selectedModels = useMemo(() => selected ? models.filter((m) => m.backend === selected.name) : [], [models, selected])
  const selectedAgg = useMemo(() => backendAgg(models, selected?.name ?? ''), [models, selected])

  return (
    <Fade pending={ovQ.isPending || statsQ.isPending}>
      <Stack gap="lg">
        <PageHeader
          title="Providers"
          subtitle="Manage connections and keep an eye on provider health."
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
            <SimpleGrid cols={{ base: 2, sm: 3, lg: 5 }} spacing="sm">
              <CompactStat label="Configured" value={fmtInt(backends.length)} />
              <CompactStat label="Enabled" value={fmtInt(enabledCount)} />
              <CompactStat label="Healthy" value={statsState === 'ready' ? fmtInt(healthyCount) : '—'} />
              <CompactStat label="Missing auth" value={fmtInt(missingAuthCount)} />
              <CompactStat label="Needs attention" value={statsState === 'ready' ? fmtInt(needsAttentionCount) : '—'} />
            </SimpleGrid>
            <Box className="provider-toolbar">
              <TextInput aria-label="Search providers" placeholder="Search providers or models…" leftSection={<IconSearch size={17} />}
                size="md" value={search} onChange={(event) => setSearch(event.currentTarget.value)} />
              <Select aria-label="Filter provider health" size="md" value={healthFilter} allowDeselect={false}
                disabled={statsState !== 'ready'} onChange={(value) => setHealthFilter(value ?? 'all')}
                data={[{ value: 'all', label: 'All providers' }, { value: 'attention', label: 'Needs attention' },
                  { value: 'healthy', label: 'Healthy' }, { value: 'no-traffic', label: 'No traffic' }]} />
            </Box>
            <Text size="xs" c="dimmed" aria-live="polite">Showing {visibleBackends.length} of {backends.length} providers · health reflects all recorded requests</Text>
            {visibleBackends.length === 0 && <EmptyState icon={<IconSearchOff size={24} />} title="No matching providers" hint="Try another name or choose a different health filter." />}
            <SimpleGrid cols={{ base: 1, md: 2, xl: 3 }} spacing="md">
              {visibleBackends.map((b) => (
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
              <Group gap="xs" wrap="wrap">
                <Text fw={700} style={{ overflowWrap: 'anywhere' }}>{selected.name}</Text>
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
  return <StatTile label={label} value={value} />
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
        zIndex={0}
        aria-hidden="true"
        style={{ flexShrink: 0, backgroundColor: ok ? 'var(--data-good)' : 'var(--data-warning)' }}
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
          color="brand"
          variant={backend.authConfigured ? 'subtle' : 'light'}
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
  const [routesExpanded, setRoutesExpanded] = useState(false)
  const catalogListId = useId()
  const routesListId = useId()
  const agg = backendAgg(models, b.name)
  const requests = agg.requests
  const uptime = agg.uptime
  const segTotal = segments.reduce((s, x) => s + x.value, 0)
  const shownModels = catalogExpanded ? (b.models ?? []) : (b.models?.slice(0, CATALOG_PREVIEW) ?? [])
  const extra = (b.models?.length ?? 0) - shownModels.length
  const shownRoutes = routesExpanded ? routes : routes.slice(0, ROUTES_PREVIEW)
  const statsReady = statsState === 'ready'

  return (
    <Card
      withBorder
      radius="lg"
      p="md"
      className="provider-card"
      miw={0}
      h="100%"
    >
      <Box className="provider-card-heading">
        <Group gap="sm" wrap="nowrap" miw={0}>
          <span className="provider-avatar" aria-hidden>{b.name.slice(0, 2).toUpperCase()}</span>
          <Box miw={0}>
            <Title order={3} size="h4" style={{ overflowWrap: 'anywhere' }}>{b.name}</Title>
            <Text size="xs" c="dimmed">{b.enabled ? 'Enabled' : 'Disabled'}</Text>
          </Box>
        </Group>
        <Button size="xs" variant="subtle" mih={44} px={8} rightSection={<IconChevronRight size={15} />}
          aria-label={`Inspect ${b.name}`} onClick={onInspect}>Details</Button>
      </Box>
      <Group justify="space-between" gap="xs" mt="md">
        <UptimeBadge uptime={statsReady ? uptime : NaN} requests={statsReady ? requests : NaN} />
        <StatusDot ok={b.catalogOK} okLabel="Catalog ready" badLabel="Catalog unavailable" />
      </Group>
      <Box className="provider-metrics">
        <Box><Text size="xs" c="dimmed">Requests</Text><Text fw={650} className="stat-value">{statsReady ? fmtInt(requests) : '—'}</Text></Box>
        <Box><Text size="xs" c="dimmed">Success</Text><Text fw={650} className="stat-value">{statsReady && requests ? pct(uptime) : '—'}</Text></Box>
        <Box><Text size="xs" c="dimmed">Models</Text><Text fw={650} className="stat-value">{b.catalogOK ? (b.models?.length ?? 0) : '—'}</Text></Box>
      </Box>
      <AuthStatus backend={b} />

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
              <TokenMixBar segments={segments} height={10} />
              <TokenLegend segments={segments} compact />
            </>
          )}
          <Text size="xs" c="dimmed" mt={segTotal > 0 ? 6 : 0}>
            {fmtInt(segTotal)} tokens · {fmtInt(agg.toolCalls)} tool calls
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
            {routes.length > ROUTES_PREVIEW && (
              <Button size="xs" variant="subtle" mih={44} aria-expanded={routesExpanded} aria-controls={routesListId} onClick={() => setRoutesExpanded((value) => !value)}>
                {routesExpanded ? 'Show fewer routes' : `Show all ${routes.length} routes`}
              </Button>
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
                <Text className="provider-model">{m.startsWith(`${b.name}/`) ? m.slice(b.name.length + 1) : m}</Text>
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
                aria-controls={catalogListId}
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
                aria-controls={catalogListId}
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
