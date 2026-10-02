import { useMemo, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Drawer,
  Group,
  Loader,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Text,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconCircleCheck, IconSearchOff, IconServerOff } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { dashboardQueries } from '../queries'
import { fmtInt } from '../format'
import { useChartPalette } from '../palette'
import UptimeBadge from '../components/UptimeBadge'
import SearchInput from '../components/SearchInput'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { providerSegments, healthState } from '../lib/stats'
import ProviderCard from '../components/catalog/ProviderCard'
import ProviderDetail from '../components/catalog/ProviderDetail'
import { backendAgg, type StatsState } from '../components/catalog/providerData'
import { Fade } from '../App'
import './catalog.css'

export default function ProvidersPage() {
  const overview = useQuery(dashboardQueries.overview())
  const stats = useQuery(dashboardQueries.stats())
  const grokUsage = useQuery(dashboardQueries.grokUsage(overview.data?.grokUsage.configured ?? false))
  const minimaxEnabled = overview.data?.minimaxUsage?.configured ?? false
  const minimaxUsage = useQuery(dashboardQueries.minimaxUsage(minimaxEnabled))
  const palette = useChartPalette()
  const mobile = useMediaQuery('(max-width: 48em)') ?? false
  const [search, setSearch] = useState('')
  const [healthFilter, setHealthFilter] = useState('all')
  const [sort, setSort] = useState('name')
  const [selectedName, setSelectedName] = useState<string | null>(null)
  const [historyRange, setHistoryRange] = useState('24h')
  const backends = useMemo(() => overview.data?.backends ?? [], [overview.data])
  const models = useMemo(() => stats.data?.models ?? [], [stats.data])
  const statsState: StatsState = stats.data ? 'ready' : stats.isPending ? 'loading' : 'unavailable'
  const segments = useMemo(() => new Map(providerSegments(models, palette.series)), [models, palette.series])
  const providers = useMemo(
    () =>
      backends.map((backend) => {
        const providerModels = models.filter((model) => model.backend === backend.name)
        const aggregate = backendAgg(providerModels, backend.name)
        const health = statsState === 'ready' ? healthState(aggregate.requests, aggregate.uptime) : 'unknown'
        const attention =
          !backend.catalogOK ||
          (!backend.hasKey && !backend.authConfigured) ||
          health === 'degraded' ||
          health === 'unhealthy'
        return { backend, models: providerModels, aggregate, health, attention }
      }),
    [backends, models, statsState],
  )
  const visible = useMemo(
    () =>
      providers
        .filter(({ backend, health, attention }) => {
          const needle = search.trim().toLowerCase()
          const matchesSearch =
            !needle ||
            [backend.name, backend.host, ...(backend.models ?? [])].some((value) =>
              value.toLowerCase().includes(needle),
            )
          const activeFilter = statsState === 'ready' ? healthFilter : 'all'
          return (
            matchesSearch &&
            (activeFilter === 'all' ||
              (activeFilter === 'attention'
                ? attention
                : activeFilter === 'enabled'
                  ? backend.enabled
                  : health === activeFilter))
          )
        })
        .sort((a, b) =>
          sort === 'traffic' && statsState === 'ready'
            ? b.aggregate.requests - a.aggregate.requests
            : sort === 'attention'
              ? Number(b.attention) - Number(a.attention) || a.backend.name.localeCompare(b.backend.name)
              : a.backend.name.localeCompare(b.backend.name),
        ),
    [providers, search, healthFilter, sort, statsState],
  )
  const selected = providers.find(({ backend }) => backend.name === selectedName) ?? null
  const history = useQuery(dashboardQueries.backendSeries(selected?.backend.name ?? null, historyRange))
  const configured = backends.length
  const enabled = backends.filter((backend) => backend.enabled).length
  const healthy = providers.filter((provider) => provider.health === 'healthy').length
  const attention = providers.filter((provider) => provider.attention).length
  const idle = providers.filter((provider) => provider.health === 'no-traffic').length
  const missingAuth = backends.filter((backend) => !backend.hasKey && !backend.authConfigured).length
  return (
    <Fade pending={overview.isPending || stats.isPending}>
      <Stack gap="lg" className="providers-page">
        <PageHeader title="Providers" subtitle="One place for every connection, catalog, and account." />
        {overview.isPending ? (
          <Group justify="center" py="xl" role="status">
            <Loader size="sm" />
            <Text size="sm">Loading providers…</Text>
          </Group>
        ) : overview.isError ? (
          <Alert color="red" title="Couldn't load providers">
            <Text size="sm">{overview.error.message}</Text>
            <Button variant="light" mih={44} mt="sm" onClick={() => overview.refetch()}>
              Retry
            </Button>
          </Alert>
        ) : backends.length === 0 ? (
          <EmptyState
            icon={<IconServerOff size={24} />}
            title="No providers configured"
            hint="Add a backend to the proxy config and it will appear here."
          />
        ) : (
          <>
            <Paper
              component="section"
              aria-label="Provider summary"
              withBorder
              radius="lg"
              className="catalog-provider-summary"
            >
              <Summary label="Configured providers" value={fmtInt(configured)} hint={`${enabled} enabled`} />
              <Summary
                label="Healthy"
                value={statsState === 'ready' ? fmtInt(healthy) : '—'}
                hint="From recorded requests"
              />
              <Summary
                label="Needs attention"
                value={statsState === 'ready' ? fmtInt(attention) : '—'}
                hint={`${missingAuth} missing authentication`}
                attention={attention > 0}
              />
              <Summary
                label="Awaiting traffic"
                value={statsState === 'ready' ? fmtInt(idle) : '—'}
                hint={statsState === 'ready' ? 'Health appears after requests' : 'Request stats unavailable'}
              />
            </Paper>
            {statsState !== 'ready' && (
              <Alert
                color="gray"
                title={statsState === 'loading' ? 'Loading request stats…' : 'Request stats unavailable'}
              >
                {statsState === 'loading'
                  ? 'Health and token mix appear once stats finish loading.'
                  : 'Health and token mix are unavailable right now. Provider configuration is still current.'}
                {statsState === 'unavailable' && (
                  <Button variant="subtle" size="xs" mih={44} onClick={() => stats.refetch()}>
                    Retry statistics
                  </Button>
                )}
              </Alert>
            )}
            <Stack gap="sm">
              <div className="catalog-provider-toolbar">
                <SearchInput
                  label="Search providers"
                  placeholder="Search providers, hosts, or models…"
                  value={search}
                  onChange={setSearch}
                />
                <Select
                  aria-label="Filter provider health"
                  value={healthFilter}
                  allowDeselect={false}
                  disabled={statsState !== 'ready'}
                  onChange={(value) => setHealthFilter(value ?? 'all')}
                  data={[
                    { value: 'all', label: 'All providers' },
                    { value: 'attention', label: 'Needs attention' },
                    { value: 'healthy', label: 'Healthy' },
                    { value: 'no-traffic', label: 'No traffic' },
                    { value: 'enabled', label: 'Enabled' },
                  ]}
                />
                <Select
                  aria-label="Sort providers"
                  value={sort}
                  allowDeselect={false}
                  onChange={(value) => setSort(value ?? 'name')}
                  data={[
                    { value: 'name', label: 'Name A–Z' },
                    { value: 'attention', label: 'Attention first' },
                    { value: 'traffic', label: 'Most requests', disabled: statsState !== 'ready' },
                  ]}
                />
              </div>
              <Group justify="space-between" gap="xs">
                <Text size="xs" c="dimmed" aria-live="polite">
                  {visible.length} of {configured} providers
                </Text>
                <Text size="xs" c="dimmed" className="catalog-health-note">
                  <IconCircleCheck size={13} />
                  Health from recorded requests
                </Text>
              </Group>
            </Stack>
            {visible.length === 0 ? (
              <Stack align="center" gap="xs">
                <EmptyState
                  icon={<IconSearchOff size={24} />}
                  title="No matching providers"
                  hint="Try another name or choose a different health filter."
                />
                <Button
                  variant="light"
                  mih={44}
                  onClick={() => {
                    setSearch('')
                    setHealthFilter('all')
                  }}
                >
                  Clear filters
                </Button>
              </Stack>
            ) : (
              <SimpleGrid cols={{ base: 1, md: 2, xl: 3 }} spacing="md">
                {visible.map(({ backend, models: providerModels }) => (
                  <ProviderCard
                    key={backend.name}
                    backend={backend}
                    models={providerModels}
                    routeCount={
                      (overview.data?.routes ?? []).filter((route) => route.backend === backend.name).length
                    }
                    segments={segments.get(backend.name) ?? []}
                    statsState={statsState}
                    grokUsage={backend.name === 'grok' ? grokUsage.data : undefined}
                    minimaxUsageQuery={
                      backend.name === 'minimax-code' && minimaxEnabled ? minimaxUsage : undefined
                    }
                    onInspect={() => setSelectedName(backend.name)}
                  />
                ))}
              </SimpleGrid>
            )}
          </>
        )}
      </Stack>
      <Drawer
        opened={!!selected}
        onClose={() => setSelectedName(null)}
        position="right"
        size={mobile ? '100%' : 'lg'}
        className="catalog-detail-drawer"
        closeButtonProps={{ ...{ 'data-autofocus': true }, 'aria-label': 'Close provider details', size: 44 }}
        styles={{ title: { minWidth: 0, flex: 1 }, close: { flexShrink: 0 } }}
        title={
          selected && (
            <Box miw={0}>
              <Text size="xs" c="dimmed">
                Provider / Connection details
              </Text>
              <Group gap="sm" mt={5}>
                <Text fw={700} className="catalog-model-name">
                  {selected.backend.name}
                </Text>
                <UptimeBadge
                  uptime={statsState === 'ready' ? selected.aggregate.uptime : NaN}
                  requests={statsState === 'ready' ? selected.aggregate.requests : NaN}
                />
              </Group>
            </Box>
          )
        }
      >
        {selected && (
          <ProviderDetail
            backend={selected.backend}
            routes={(overview.data?.routes ?? []).filter((route) => route.backend === selected.backend.name)}
            models={selected.models}
            statsState={statsState}
            grokUsage={selected.backend.name === 'grok' ? grokUsage.data : undefined}
            minimaxUsageQuery={
              selected.backend.name === 'minimax-code' && minimaxEnabled ? minimaxUsage : undefined
            }
            series={history.data?.series}
            seriesLoading={history.isPending}
            seriesError={history.error?.message}
            range={historyRange}
            onRangeChange={setHistoryRange}
          />
        )}
      </Drawer>
    </Fade>
  )
}

function Summary({
  label,
  value,
  hint,
  attention = false,
}: {
  label: string
  value: string
  hint: string
  attention?: boolean
}) {
  return (
    <Box className="catalog-provider-summary-item" data-attention={attention}>
      <Text size="xs" fw={550} c="dimmed">
        {label}
      </Text>
      <Text className="catalog-summary-number tabular">{value}</Text>
      <Text size="xs" c="dimmed">
        {hint}
      </Text>
    </Box>
  )
}
