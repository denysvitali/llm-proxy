import {
  Accordion,
  Alert,
  Badge,
  Box,
  Code,
  Divider,
  Group,
  Loader,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage, MiniMaxUsage, ModelStat, OverviewBackend, StatsSeries } from '../../api'
import { fmtInt, fmtSec, fmtTps } from '../../format'
import { healthState } from '../../lib/stats'
import GrokUsageCompact from '../GrokUsageCompact'
import MiniMaxUsageCard from '../MiniMaxUsageCard'
import StatTile from '../StatTile'
import UptimeBadge from '../UptimeBadge'
import { TimeRangeControl } from '../TimeRangeControl'
import { HistoryBarChart, HistoryLineChart, historyData, historyFormatters } from '../HistoryCharts'
import { AuthStatus, StatusDot, CardSection } from './ProviderStatus'
import { pct, type StatsState } from './providerData'

export default function ProviderDetail({
  backend,
  routes,
  models,
  statsState,
  grokUsage,
  minimaxUsageQuery,
  series,
  seriesLoading,
  seriesError,
  range,
  onRangeChange,
}: {
  backend: OverviewBackend
  routes: { model: string; backend: string; upstream: string }[]
  models: ModelStat[]
  statsState: StatsState
  grokUsage?: GrokUsage
  minimaxUsageQuery?: UseQueryResult<MiniMaxUsage, Error>
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
          <Alert variant="light" color={healthCopy[health].color} title={healthCopy[health].title}>
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
            <StatusDot ok={backend.catalogOK} okLabel="catalog ok" badLabel="catalog unavailable" />
          </Group>
        </CardSection>

        {(backend.models?.length ?? 0) > 0 && (
          <details className="provider-detail-list">
            <summary>Available models · {backend.models?.length}</summary>
            <Stack gap="xs" mt="sm">
              {backend.models?.map((model) => (
                <Group key={model} gap="xs" wrap="nowrap" align="flex-start">
                  <Code style={{ overflowWrap: 'anywhere', minWidth: 0, flex: 1 }}>{model}</Code>
                  {backend.modelCredits?.[model] && (
                    <Badge size="xs" variant="light" color="violet">{backend.modelCredits[model]}</Badge>
                  )}
                </Group>
              ))}
            </Stack>
          </details>
        )}

        {routes.length > 0 && (
          <details className="provider-detail-list">
            <summary>Routes · {routes.length}</summary>
            <Stack gap="xs" mt="sm">
              {routes.map((route) => (
                <Code key={`${route.model}/${route.upstream}`} style={{ overflowWrap: 'anywhere' }}>
                  {route.model} → {route.upstream || '(as requested)'}
                </Code>
              ))}
            </Stack>
          </details>
        )}

        {minimaxUsageQuery && <MiniMaxUsageCard query={minimaxUsageQuery} />}

        {backend.name === 'grok' && grokUsage && <GrokUsageCompact usage={grokUsage} />}

        <Text size="xs" c="dimmed">
          All recorded traffic · totals are independent of the history range
        </Text>
        <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
          <StatTile label="Requests" value={statsReady ? fmtInt(requests) : '—'} />
          <StatTile label="Uptime" value={statsReady && requests ? pct(uptime) : '—'} />
          <StatTile label="Tracked models" value={statsReady ? fmtInt(models.length) : '—'} />
          <StatTile
            label="Tool err rate"
            value={statsReady && toolCalls ? pct(toolErrors / toolCalls) : '—'}
          />
        </SimpleGrid>

        <Accordion variant="separated" radius="md">
          <Accordion.Item value="history">
            <Accordion.Control>Performance history</Accordion.Control>
            <Accordion.Panel>
              <Stack gap="md">
                <Group justify="space-between" align="center" wrap="wrap" gap="xs">
                  <Title order={5}>History</Title>
                  <TimeRangeControl value={range} onChange={onRangeChange} />
                </Group>

                {seriesLoading && !series ? (
                  <Group justify="center" py="xl">
                    <Loader size="sm" />
                  </Group>
                ) : seriesError && !series ? (
                  <Text size="sm" c="dimmed">
                    History charts are unavailable for this provider right now. The cumulative totals above
                    still reflect all recorded traffic.
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
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>

        <Divider my="xs" />
        <Title order={5}>Model performance</Title>
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
            No requests recorded for this provider yet. Once traffic flows through the proxy, per-model uptime
            and latency appear here.
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={520}>
            <Table
              highlightOnHover
              verticalSpacing="sm"
              horizontalSpacing="sm"
              style={{ fontVariantNumeric: 'tabular-nums' }}
            >
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
                {[...models]
                  .sort((a, b) => b.requests - a.requests)
                  .map((model) => (
                    <Table.Tr key={`${model.backend}/${model.model}`}>
                      <Table.Td>
                        <Code style={{ overflowWrap: 'anywhere' }}>{model.model}</Code>
                      </Table.Td>
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
