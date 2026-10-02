import { useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Chip,
  Drawer,
  Group,
  Loader,
  Paper,
  Select,
  Stack,
  Tabs,
  Text,
  Title,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import {
  IconArrowDown,
  IconArrowUp,
  IconInboxOff,
  IconSearchOff,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { dashboardQueries } from '../queries'
import { fmtInt, fmtSec } from '../format'
import { useChartPalette } from '../palette'
import { CatalogSummary } from '../components/catalog/CatalogSummary'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import SearchInput from '../components/SearchInput'
import UptimeBadge from '../components/UptimeBadge'
import ModelCatalog from '../components/catalog/ModelCatalog'
import ModelDetail from '../components/catalog/ModelDetail'
import ModelTraffic from '../components/catalog/ModelTraffic'
import {
  compareModels,
  hasModelErrors,
  sortOptions,
  type ModelSort,
  type SortKey,
} from '../components/catalog/modelData'
import { Fade } from '../App'
import './catalog.css'

export default function ModelsPage() {
  const query = useQuery(dashboardQueries.stats())
  const models = useMemo(() => query.data?.models ?? [], [query.data])
  const palette = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const [filter, setFilter] = useState('')
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [sort, setSort] = useState<ModelSort>({ key: 'requests', dir: -1 })
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const selected = models.find((model) => `${model.backend}/${model.model}` === selectedId) ?? null
  const [historyRange, setHistoryRange] = useState('24h')
  const history = useQuery(dashboardQueries.modelSeries(selected?.backend, selected?.model, historyRange))
  const rows = useMemo(
    () =>
      models
        .filter((model) =>
          `${model.backend}/${model.model}`.toLowerCase().includes(filter.trim().toLowerCase()),
        )
        .filter((model) => !errorsOnly || hasModelErrors(model))
        .sort((a, b) => compareModels(a, b, sort)),
    [models, filter, errorsOnly, sort],
  )
  const summary = useMemo(() => {
    const latencies = models
      .map((model) => model.ttft_seconds.p50)
      .filter((value) => Number.isFinite(value) && value > 0)
      .sort((a, b) => a - b)
    const middle = Math.floor(latencies.length / 2)
    return {
      requests: models.reduce((total, model) => total + model.requests, 0),
      latency:
        latencies.length === 0
          ? NaN
          : latencies.length % 2
            ? latencies[middle]
            : (latencies[middle - 1] + latencies[middle]) / 2,
      errors: models.filter(hasModelErrors).length,
    }
  }, [models])
  function toggleSort(key: SortKey) {
    setSort((previous) => ({
      key,
      dir: previous.key === key ? (previous.dir === 1 ? -1 : 1) : key === 'model' ? 1 : -1,
    }))
  }
  function resetFilters() {
    setFilter('')
    setErrorsOnly(false)
  }
  return (
    <Fade pending={query.isPending}>
      <Stack gap="lg" className="models-page">
        <PageHeader title="Models" subtitle="Explore available models and inspect their recorded performance." />
        <CatalogSummary label="Model traffic summary" items={[
          { label: 'Models tracked', value: query.data ? fmtInt(models.length) : '—', hint: 'With recorded activity' },
          { label: 'Requests', value: query.data ? fmtInt(summary.requests) : '—', hint: 'Across all models' },
          { label: 'First token', value: fmtSec(summary.latency), hint: 'Median model p50' },
          { label: 'With errors', value: query.data ? fmtInt(summary.errors) : '—', hint: 'Request or tool failures', attention: summary.errors > 0 },
        ]} />
        <Tabs defaultValue="catalog" className="catalog-workspace-tabs">
          <Tabs.List mb="md" aria-label="Model workspace">
            <Tabs.Tab value="catalog">Available models</Tabs.Tab>
            <Tabs.Tab value="traffic">Recorded traffic</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="catalog"><ModelCatalog /></Tabs.Panel>
          <Tabs.Panel value="traffic">
            <Paper
              component="section"
              withBorder
              radius="lg"
              className="catalog-panel catalog-traffic-panel"
              aria-label="Recorded model traffic"
            >
              <Group justify="space-between" gap="xs" mb="md">
                <Box>
                  <Title order={2} size="h4">
                    Recorded traffic
                  </Title>
                  <Text size="xs" c="dimmed" mt={3}>
                    All-time totals · latency and throughput show p50
                  </Text>
                </Box>
                <Badge variant="light" color="gray">
                  {query.data ? models.length : '—'} tracked
                </Badge>
              </Group>
              <div className="catalog-traffic-toolbar">
                <SearchInput
                  label="Filter backend or model"
                  placeholder="Filter backend or model…"
                  value={filter}
                  onChange={setFilter}
                />
                <div className="catalog-sort-controls">
                  <Select
                    aria-label="Sort models by"
                    value={sort.key}
                    data={sortOptions}
                    allowDeselect={false}
                    onChange={(value) =>
                      value && setSort({ key: value as SortKey, dir: value === 'model' ? 1 : -1 })
                    }
                  />
                  <ActionIcon
                    variant="default"
                    size={44}
                    aria-label={`Sort direction: ${sort.dir === 1 ? 'ascending' : 'descending'}. Switch to ${sort.dir === 1 ? 'descending' : 'ascending'}.`}
                    onClick={() => setSort((previous) => ({ ...previous, dir: previous.dir === 1 ? -1 : 1 }))}
                  >
                    {sort.dir === 1 ? <IconArrowUp size={17} /> : <IconArrowDown size={17} />}
                  </ActionIcon>
                </div>
              </div>
              <Group justify="space-between" gap="xs" my="sm">
                <Text size="xs" c="dimmed" aria-live="polite">
                  {query.isPending
                    ? 'Loading tracked models…'
                    : query.isError
                      ? 'Model statistics unavailable.'
                      : `${rows.length} of ${models.length} tracked models`}
                </Text>
                <Chip checked={errorsOnly} onChange={setErrorsOnly} size="sm" variant="light">
                  Errors only
                </Chip>
              </Group>
              {query.isPending ? (
                <Group py="xl" justify="center" role="status">
                  <Loader size="sm" />
                  <Text size="sm">Loading model statistics…</Text>
                </Group>
              ) : query.isError ? (
                <Alert color="red" title="Couldn't load model stats">
                  <Stack gap="xs" align="flex-start">
                    <Text size="sm">Model statistics are temporarily unavailable. {query.error.message}</Text>
                    <Button variant="light" mih={44} onClick={() => query.refetch()}>
                      Retry
                    </Button>
                  </Stack>
                </Alert>
              ) : rows.length === 0 ? (
                <Stack gap="xs" align="center">
                  <EmptyState
                    icon={models.length ? <IconSearchOff size={24} /> : <IconInboxOff size={24} />}
                    title={models.length ? 'No models match that filter' : 'No model traffic yet'}
                    hint={
                      models.length
                        ? 'Try another model name or clear your filters.'
                        : 'Once requests reach the proxy, their performance appears here.'
                    }
                  />
                  {models.length > 0 && (
                    <Button variant="light" mih={44} onClick={resetFilters}>
                      Clear filters
                    </Button>
                  )}
                </Stack>
              ) : (
                <ModelTraffic
                  models={rows}
                  mobile={isMobile}
                  sort={sort}
                  onSort={toggleSort}
                  onInspect={(model) => setSelectedId(`${model.backend}/${model.model}`)}
                />
              )}
            </Paper>
          </Tabs.Panel>
        </Tabs>
      </Stack>
      <Drawer
        opened={!!selected}
        onClose={() => setSelectedId(null)}
        position="right"
        size={isMobile ? '100%' : 'lg'}
        className="catalog-detail-drawer"
        closeButtonProps={{ ...{ 'data-autofocus': true }, 'aria-label': 'Close model details', size: 44 }}
        styles={{ title: { minWidth: 0, flex: 1 }, close: { flexShrink: 0 } }}
        title={
          selected && (
            <Box miw={0}>
              <Text size="xs" c="dimmed" fw={600}>
                {selected.backend} / Model performance
              </Text>
              <Text fw={700} mt={4} className="catalog-model-name">
                {selected.model}
              </Text>
              <Box mt={6}>
                <UptimeBadge uptime={selected.uptime} requests={selected.requests} />
              </Box>
            </Box>
          )
        }
      >
        {selected && (
          <ModelDetail
            stat={selected}
            colors={palette.series}
            series={history.data?.series}
            historyPending={history.isPending}
            historyError={history.isError}
            historyFetching={history.isFetching}
            onRetryHistory={() => history.refetch({ cancelRefetch: false })}
            range={historyRange}
            onRangeChange={setHistoryRange}
          />
        )}
      </Drawer>
    </Fade>
  )
}
