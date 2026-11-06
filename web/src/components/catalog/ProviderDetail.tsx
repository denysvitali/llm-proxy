import { useState } from 'react'
import {
  Alert,
  Badge,
  Box,
  Button,
  Code,
  Group,
  Loader,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage, MiniMaxUsage, ModelStat, OpenDesignUsage, OverviewBackend, StatsSeries } from '../../api'
import { fmtInt, fmtSec, fmtTps } from '../../format'
import { healthState } from '../../lib/stats'
import GrokUsageCompact from '../GrokUsageCompact'
import MiniMaxUsageCard from '../MiniMaxUsageCard'
import OpenDesignUsageCard from '../OpenDesignUsageCard'
import UptimeBadge from '../UptimeBadge'
import { TimeRangeControl } from '../TimeRangeControl'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import { AuthStatus, StatusDot, CardSection } from './ProviderStatus'
import { SuccessMeter } from './SuccessMeter'
import { pct, type ProviderAggregate, type StatsState } from './providerData'

// A pill-segmented switch, built from real buttons so the drawer's disclosure
// stays a named button ("Performance history") as well as a visual pill group.
// (Mantine Tabs renders role="tab", which the screenshot harness does not
// resolve as a button.)
function PillSwitch({
  value,
  onChange,
  options,
}: {
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <div className="catalog-pill-switch" role="group" aria-label="Provider detail views">
      {options.map((option) => (
        <Button
          key={option.value}
          size="xs"
          variant="subtle"
          color="gray"
          className="catalog-pill"
          aria-pressed={value === option.value}
          data-active={value === option.value ? 'true' : undefined}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </Button>
      ))}
    </div>
  )
}

// Compact stat: micro-label over a tabular figure. Used in the summary band
// and in the per-model table, so the drawer speaks one number language.
function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Box miw={0} className="catalog-detail-stat">
      <Text className="micro-label catalog-detail-stat-label">{label}</Text>
      <Text className="catalog-detail-stat-value tabular">{value}</Text>
      {hint && (
        <Text size="xs" c="dimmed" className="catalog-detail-stat-hint">
          {hint}
        </Text>
      )}
    </Box>
  )
}

export default function ProviderDetail({
  backend,
  routes,
  models,
  aggregate,
  statsState,
  grokUsage,
  minimaxUsageQuery,
  openDesignUsageQuery,
  series,
  seriesLoading,
  seriesError,
  range,
  onRangeChange,
}: {
  backend: OverviewBackend
  routes: { model: string; backend: string; upstream: string }[]
  models: ModelStat[]
  aggregate: ProviderAggregate
  statsState: StatsState
  grokUsage?: GrokUsage
  minimaxUsageQuery?: UseQueryResult<MiniMaxUsage, Error>
  openDesignUsageQuery?: UseQueryResult<OpenDesignUsage, Error>
  series?: StatsSeries
  seriesLoading?: boolean
  seriesError?: string
  range: string
  onRangeChange: (value: string) => void
}) {
  const { requests, toolCalls, toolErrors, uptime } = aggregate
  const statsReady = statsState === 'ready'
  const [view, setView] = useState<'overview' | 'history'>('overview')

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
      body: `Some upstream requests to this provider are failing — ${pct(1 - uptime)} of ${requests.toLocaleString('en-US')} requests did not succeed. Check the per-model table for the worst offenders.`,
      color: 'yellow' as const,
    },
    unhealthy: {
      title: 'Unhealthy',
      body: `A large share of upstream requests to this provider failed — ${pct(1 - uptime)} of ${requests.toLocaleString('en-US')}. Recent failures are listed on the Overview page.`,
      color: 'red' as const,
    },
  }

  return (
    <div className="catalog-detail">
      <Stack gap="lg" miw={0} className="catalog-detail-stack">
        {statsReady && health !== 'healthy' && (
          <Alert variant="light" color={healthCopy[health].color} title={healthCopy[health].title}>
            {healthCopy[health].body}
          </Alert>
        )}

        {!statsReady && (
          <Alert variant="light" color="gray" title="Per-model stats unavailable">
            {statsState === 'loading'
              ? 'Loading model stats…'
              : 'Per-model stats are temporarily unavailable; the configuration summary below is still current.'}
          </Alert>
        )}

        <CardSection title="Configuration">
          <Code className="catalog-config-host">{backend.host}</Code>
          <Badge size="sm" variant="light" color={backend.enabled ? 'teal' : 'gray'}>
            {backend.enabled ? 'enabled' : 'disabled'}
          </Badge>
          <AuthStatus backend={backend} compact />
          <StatusDot ok={backend.catalogOK} okLabel="catalog ok" badLabel="catalog unavailable" />
        </CardSection>

        {(backend.models?.length ?? 0) > 0 && (
          <section className="catalog-detail-section">
            <Title order={3} className="catalog-detail-label">
              Available models · {backend.models?.length}
            </Title>
            <div
              className="catalog-model-scroll"
              role="region"
              aria-label="Available models for this provider"
              tabIndex={0}
            >
              {backend.models?.map((model) => (
                <Group key={model} gap="xs" wrap="nowrap" align="flex-start" className="catalog-model-scroll-row">
                  <Code className="catalog-model-scroll-code">{model}</Code>
                  {backend.modelCredits?.[model] && (
                    <Badge size="xs" variant="light" color="violet" className="catalog-credit-badge">
                      {backend.modelCredits[model]}
                    </Badge>
                  )}
                </Group>
              ))}
            </div>
          </section>
        )}

        {routes.length > 0 && (
          <section className="catalog-detail-section">
            <Title order={3} className="catalog-detail-label">
              Routes · {routes.length}
            </Title>
            <div
              className="catalog-model-scroll catalog-route-scroll"
              role="region"
              aria-label="Configured routes for this provider"
              tabIndex={0}
            >
              {routes.map((route) => (
                <Code key={`${route.model}/${route.upstream}`} className="catalog-route-row">
                  {route.model} → {route.upstream || '(as requested)'}
                </Code>
              ))}
            </div>
          </section>
        )}

        {(minimaxUsageQuery || openDesignUsageQuery || (backend.name === 'grok' && grokUsage)) && (
          <div className="catalog-detail-usage">
            {minimaxUsageQuery && <MiniMaxUsageCard query={minimaxUsageQuery} />}
            {openDesignUsageQuery && <OpenDesignUsageCard query={openDesignUsageQuery} />}
            {backend.name === 'grok' && grokUsage && <GrokUsageCompact usage={grokUsage} />}
          </div>
        )}

        <section className="catalog-detail-section" aria-labelledby="provider-traffic-heading">
          <Title order={3} id="provider-traffic-heading" className="catalog-detail-label">
            All recorded traffic
          </Title>
          <Text size="xs" c="dimmed" className="catalog-detail-hint">
            Totals are independent of the history range
          </Text>
          <SimpleGrid cols={{ base: 2, sm: 4 }} spacing={0} verticalSpacing={0} className="catalog-detail-stats">
            <Stat label="Requests" value={statsReady ? fmtInt(requests) : '—'} />
            <Stat label="Uptime" value={statsReady && requests ? pct(uptime) : '—'} />
            <Stat label="Tracked models" value={statsReady ? fmtInt(models.length) : '—'} />
            <Stat label="Tool err rate" value={statsReady && toolCalls ? pct(toolErrors / toolCalls) : '—'} />
          </SimpleGrid>
          {statsReady && toolCalls > 0 && (
            <Text size="xs" c="dimmed">
              {fmtInt(toolCalls)} tool calls · {pct(toolErrors / toolCalls)} errors
            </Text>
          )}
        </section>

        <PillSwitch
          value={view}
          onChange={(value) => setView(value as 'overview' | 'history')}
          options={[
            { value: 'overview', label: 'Overview' },
            { value: 'history', label: 'Performance history' },
          ]}
        />

        {view === 'overview' ? (
          <section className="catalog-detail-section">
            <Group justify="space-between" align="center" wrap="wrap" gap="xs">
              <Title order={3} className="catalog-detail-label">
                Model performance
              </Title>
              <Text size="xs" c="dimmed">
                All recorded traffic
              </Text>
            </Group>
            {statsState === 'loading' ? (
              <Text size="sm" c="dimmed" role="status">
                Loading model stats…
              </Text>
            ) : statsState === 'unavailable' ? (
              <Text size="sm" c="dimmed">
                Model stats are temporarily unavailable.
              </Text>
            ) : models.length === 0 ? (
              <Text size="sm" c="dimmed">
                No requests recorded for this provider yet. Once traffic flows through the proxy,
                per-model uptime and latency appear here.
              </Text>
            ) : (
              <Table.ScrollContainer minWidth={480} className="catalog-model-table-scroll">
                <Table
                  className="catalog-model-table"
                  verticalSpacing="xs"
                  horizontalSpacing="sm"
                  aria-label="Per-model performance"
                >
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>Model</Table.Th>
                      <Table.Th ta="right">Req</Table.Th>
                      <Table.Th>Success</Table.Th>
                      <Table.Th ta="right">TTFT</Table.Th>
                      <Table.Th ta="right">E2E</Table.Th>
                      <Table.Th ta="right">tok/s</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {[...models]
                      .sort((a, b) => b.requests - a.requests)
                      .map((model) => (
                        <Table.Tr key={`${model.backend}/${model.model}`} className="catalog-model-table-row">
                          <Table.Td>
                            <Code className="catalog-model-table-id">{model.model}</Code>
                          </Table.Td>
                          <Table.Td ta="right" className="tabular">
                            {fmtInt(model.requests)}
                          </Table.Td>
                          <Table.Td>
                            <Group gap={6} wrap="nowrap">
                              <SuccessMeter rate={model.successes / model.requests} requests={model.requests} compact />
                              <UptimeBadge uptime={model.uptime} requests={model.requests} />
                            </Group>
                          </Table.Td>
                          <Table.Td ta="right" className="tabular">
                            {fmtSec(model.ttft_seconds.p50)}
                          </Table.Td>
                          <Table.Td ta="right" className="tabular">
                            {fmtSec(model.e2e_seconds.p50)}
                          </Table.Td>
                          <Table.Td ta="right" className="tabular">
                            {fmtTps(model.throughput_tps.p50)}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </section>
        ) : (
          <section className="catalog-detail-section">
            <Group justify="space-between" align="center" wrap="wrap" gap="sm">
              <Text className="micro-label">History range</Text>
              <TimeRangeControl value={range} onChange={onRangeChange} />
            </Group>
            {seriesLoading && !series ? (
              <Group justify="center" py="xl" role="status" aria-label="Loading provider history">
                <Loader size="sm" />
              </Group>
            ) : seriesError && !series ? (
              <Text size="sm" c="dimmed">
                History charts are unavailable for this provider right now. The cumulative totals
                elsewhere still reflect all recorded traffic.
              </Text>
            ) : (
              <Stack gap="md">
                <HistoryLineChart
                  title="Latency"
                  description="Median first byte and full response"
                  data={historyData([series?.ttft_p50, series?.e2e_p50])}
                  height={118}
                  series={[
                    { name: 'series0', label: 'First byte', formatter: historyFormatters.seconds },
                    { name: 'series1', label: 'Full response', formatter: historyFormatters.seconds },
                  ]}
                />
                <HistoryLineChart
                  title="Throughput"
                  description="Median output rate"
                  data={historyData([series?.throughput_p50])}
                  height={118}
                  series={[{ name: 'series0', label: 'Tokens/sec', formatter: historyFormatters.tps }]}
                />
                <HistoryBarChart
                  title="Requests"
                  description="Requests per interval"
                  points={series?.requests ?? []}
                  height={100}
                />
                <HistoryLineChart
                  title="Token volume"
                  description="Input and output tokens per interval"
                  data={historyData([series?.tokens_in, series?.tokens_out])}
                  height={118}
                  series={[
                    { name: 'series0', label: 'Input', formatter: historyFormatters.count },
                    { name: 'series1', label: 'Output', formatter: historyFormatters.count },
                  ]}
                />
                <HistoryBarChart
                  title="Tool calls"
                  description="Observed calls per interval"
                  points={series?.tool_calls ?? []}
                  height={100}
                />
              </Stack>
            )}
          </section>
        )}
      </Stack>
    </div>
  )
}
