import { useMemo, useState } from 'react'
import { Accordion, Badge, Box, Button, Card, Group, SimpleGrid, Skeleton, Stack, Text, Title } from '@mantine/core'
import { IconActivity, IconArrowUpRight, IconBolt, IconCoins, IconInboxOff, IconShieldCheck, IconWallet } from '@tabler/icons-react'
import { useMediaQuery } from '@mantine/hooks'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { dashboardQueries } from '../queries'
import type { ModelStat, Overview } from '../api'
import GrokUsageCard from '../components/GrokUsageCard'
import ZcodeUsageCard from '../components/ZcodeUsageCard'
import MiniMaxUsageCard from '../components/MiniMaxUsageCard'
import { PageHeader } from '../components/PageHeader'
import { PageSection } from '../components/PageSection'
import { clampRate, fmtInt, fmtTps } from '../format'
import StatTile from '../components/StatTile'
import { EmptyState } from '../components/EmptyState'
import Fade from '../components/Fade'
import ErrorRetryCard from '../components/ErrorRetryCard'
import OverviewHistory from '../components/overview/OverviewHistory'
import OverviewTraffic from '../components/overview/OverviewTraffic'
import { RecentRequestsCard, UpstreamErrorsCard } from '../components/overview/OverviewActivity'
import './overview.css'

const NO_MODELS: ModelStat[] = []

export default function OverviewPage() {
  const statsQ = useQuery(dashboardQueries.stats())
  const ovQ = useQuery(dashboardQueries.overview())
  const ov = ovQ.data
  const grokUsageEnabled = ov?.grokUsage.configured ?? false
  const zcodeUsageEnabled = ov?.zcodeUsage.configured ?? false
  const minimaxUsageEnabled = ov?.minimaxUsage?.configured ?? false
  const grokUsageQ = useQuery(dashboardQueries.grokUsage(grokUsageEnabled))
  const zcodeUsageQ = useQuery(dashboardQueries.zcodeUsage(zcodeUsageEnabled))
  const minimaxUsageQ = useQuery(dashboardQueries.minimaxUsage(minimaxUsageEnabled))
  const [range, setRange] = useState('24h')
  const seriesQ = useQuery(dashboardQueries.series(range))
  const errorsQ = useQuery(dashboardQueries.errors())
  const requestsQ = useQuery(dashboardQueries.requests())
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const models = statsQ.data?.models ?? NO_MODELS
  const totals = useMemo(() => {
    const speeds = models.map((model) => model.throughput_tps.p50).filter((speed) => Number.isFinite(speed) && speed > 0).sort((a, b) => a - b)
    const middle = Math.floor(speeds.length / 2)
    return {
      requests: models.reduce((sum, model) => sum + model.requests, 0),
      successes: models.reduce((sum, model) => sum + model.successes, 0),
      input: models.reduce((sum, model) => sum + model.input_tokens, 0),
      output: models.reduce((sum, model) => sum + model.output_tokens, 0),
      toolCalls: models.reduce((sum, model) => sum + model.tool_calls, 0),
      toolErrors: models.reduce((sum, model) => sum + model.tool_errors, 0),
      speed: speeds.length ? (speeds.length % 2 ? speeds[middle] : (speeds[middle - 1] + speeds[middle]) / 2) : Number.NaN,
    }
  }, [models])
  const usageNames = [grokUsageEnabled && 'Grok', zcodeUsageEnabled && 'ZCode', minimaxUsageEnabled && 'MiniMax'].filter(Boolean)

  const coreFailed = statsQ.isError && ovQ.isError && seriesQ.isError && errorsQ.isError && requestsQ.isError
  return (
    <Fade pending={statsQ.isPending || ovQ.isPending}>
      <Stack gap="md" className="overview-page">
        <PageHeader title="Overview" subtitle="Traffic, performance, and the health of your gateway."
          extra={<Button component={Link} to="/models" variant="default" size="sm" rightSection={<IconArrowUpRight size={16} />}>Explore models</Button>} />

        {coreFailed ? (
          <ErrorRetryCard title="Couldn't load the overview"
            message="Traffic, configuration, and recent activity could not be refreshed."
            onRetry={() => {
              void statsQ.refetch({ cancelRefetch: false })
              void ovQ.refetch({ cancelRefetch: false })
              void seriesQ.refetch({ cancelRefetch: false })
              void errorsQ.refetch({ cancelRefetch: false })
              void requestsQ.refetch({ cancelRefetch: false })
            }}
            retrying={statsQ.isFetching || ovQ.isFetching || seriesQ.isFetching || errorsQ.isFetching || requestsQ.isFetching} />
        ) : ovQ.isError ? (
          <ErrorRetryCard title="Instance details unavailable" message="Configuration and subscription visibility could not be refreshed. Traffic statistics are loaded separately."
            onRetry={() => ovQ.refetch({ cancelRefetch: false })} retrying={ovQ.isFetching} />
        ) : null}

        <section aria-label="Traffic summary">
          {statsQ.data ? <>
            <SimpleGrid cols={{ base: 2, md: 4 }} spacing="sm" className="overview-stat-grid">
              <StatTile label="Requests" value={fmtInt(totals.requests)} hint={`${fmtInt(totals.successes)} succeeded`} icon={<IconActivity size={16} />} />
              <StatTile label="Success rate" value={totals.requests ? `${(100 * totals.successes / totals.requests).toFixed(1)}%` : '—'}
                hint={totals.requests ? `${fmtInt(totals.requests - totals.successes)} unsuccessful` : 'No recorded attempts'} icon={<IconShieldCheck size={16} />} accent={totals.requests && totals.successes / totals.requests < 0.99 ? 'orange' : undefined} />
              <StatTile label="Tokens served" value={fmtInt(totals.input + totals.output)} hint={`${fmtInt(totals.input)} in · ${fmtInt(totals.output)} out`} icon={<IconCoins size={16} />} />
              <StatTile label="Median tok/s" value={fmtTps(totals.speed)} hint="Median of active model p50s" icon={<IconBolt size={16} />} />
            </SimpleGrid>
            <Group className="overview-summary-footer" justify="space-between" gap="xs">
              <Text size="xs" c="dimmed">{models.filter((model) => model.requests > 0).length} {models.filter((model) => model.requests > 0).length === 1 ? 'model' : 'models'} with traffic</Text>
              <Group gap="md"><Text size="xs" c="dimmed"><Text span inherit fw={600}>{fmtInt(totals.toolCalls)}</Text> tool calls</Text><Text size="xs" style={{ color: totals.toolErrors ? 'var(--data-critical)' : undefined }} c={totals.toolErrors ? undefined : 'dimmed'}><Text span inherit fw={600}>{fmtInt(totals.toolErrors)}</Text> tool errors{totals.toolCalls > 0 ? ` · ${(100 * clampRate(totals.toolErrors / totals.toolCalls)).toFixed(1)}%` : ''}</Text></Group>
            </Group>
          </> : statsQ.isPending ? (
            <SimpleGrid cols={{ base: 2, md: 4 }} spacing="sm" className="overview-stat-grid">
              {Array.from({ length: 4 }, (_, i) => <Skeleton key={i} height={108} radius="md" />)}
            </SimpleGrid>
          ) : null}
          {!coreFailed && statsQ.isError ? (
            <ErrorRetryCard title="Couldn't load proxy statistics" message={statsQ.error instanceof Error ? `Model statistics are temporarily unavailable. ${statsQ.error.message}` : 'Model statistics are temporarily unavailable.'}
              onRetry={() => statsQ.refetch({ cancelRefetch: false })} retrying={statsQ.isFetching} />
          ) : !coreFailed && models.length === 0 && !statsQ.isPending ? (
            <EmptyState icon={<IconInboxOff size={20} />} title="No model traffic yet" hint="Send a request through the proxy and per-model stats will land here." />
          ) : null}
        </section>

        {!coreFailed && (
          <div className="overview-monitor">
            <OverviewHistory query={seriesQ} range={range} onRangeChange={setRange} />
            <Stack gap="sm" className="overview-errors">
              {errorsQ.isError && <ErrorRetryCard title="Couldn't refresh upstream errors" message={errorsQ.data ? 'Showing the last available errors; this list may be out of date.' : 'The error feed is unavailable. This does not mean there were no failures.'}
                onRetry={() => errorsQ.refetch({ cancelRefetch: false })} retrying={errorsQ.isFetching} />}
              {errorsQ.data ? <UpstreamErrorsCard errors={errorsQ.data.errors ?? []} /> : errorsQ.isPending ? <Skeleton height={240} radius="md" /> : null}
            </Stack>
          </div>
        )}

        {!coreFailed && usageNames.length > 0 && <Accordion variant="separated" radius="md" className="overview-subscriptions" value="subscriptions" onChange={() => {}}>
          <Accordion.Item value="subscriptions">
            <Accordion.Control icon={<IconWallet size={18} stroke={1.6} />}>
              <Group justify="space-between" gap="xs" pr="sm">
                <Box><Text size="sm" fw={600}>Subscription usage</Text><Text size="xs" c="dimmed">{usageNames.join(' · ')} account quotas</Text></Box>
                <Badge variant="light" color="gray" tt="none">{usageNames.length} accounts</Badge>
              </Group>
            </Accordion.Control>
            <Accordion.Panel><SimpleGrid cols={{ base: 1, lg: Math.min(usageNames.length, 2) }} spacing="md" className="overview-usage-grid">
              {grokUsageEnabled && <GrokUsageCard query={grokUsageQ} />}
              {zcodeUsageEnabled && <ZcodeUsageCard query={zcodeUsageQ} />}
              {minimaxUsageEnabled && <Box style={usageNames.length === 3 ? { gridColumn: '1 / -1' } : undefined}><MiniMaxUsageCard query={minimaxUsageQ} /></Box>}
            </SimpleGrid></Accordion.Panel>
          </Accordion.Item>
        </Accordion>}

        {!coreFailed && (
          <PageSection title="Recent requests" description="Retained upstream attempts · updates live and every 30 seconds · independent of chart range">
            <Stack gap="sm">
              {requestsQ.isError && <ErrorRetryCard title="Couldn't refresh recent requests" message={requestsQ.data ? 'Showing the last available attempts; this list may be out of date.' : 'Request history could not be loaded. Retry to check recent attempts.'}
                onRetry={() => requestsQ.refetch({ cancelRefetch: false })} retrying={requestsQ.isFetching} />}
              {(requestsQ.data || requestsQ.isPending) && <RecentRequestsCard requests={requestsQ.data?.requests ?? []} loading={requestsQ.isPending} isMobile={isMobile} />}
            </Stack>
          </PageSection>
        )}

        {!coreFailed && ov && <GatewaySnapshot overview={ov} />}
        {!coreFailed && statsQ.data && <OverviewTraffic models={models} />}
      </Stack>
    </Fade>
  )
}

function GatewaySnapshot({ overview }: { overview: Overview }) {
  const enabled = overview.backends.filter((backend) => backend.enabled)
  const needsAttention = (backend: Overview['backends'][number]) => (!backend.hasKey && !backend.authConfigured) || !backend.catalogOK
  const attention = enabled.filter(needsAttention)
  const ordered = [...attention, ...enabled.filter((backend) => !needsAttention(backend))]
  return (
    <aside className="overview-gateway" aria-label="Gateway configuration summary">
      <div className="overview-gateway-heading">
        <Title order={2}>Gateway readiness</Title>
        <Group gap="xs">
          <Badge color={attention.length ? 'yellow' : 'gray'} variant="light" tt="none">{attention.length ? `${attention.length} need attention` : enabled.length ? 'Catalogs ready' : 'No enabled providers'}</Badge>
          <Text size="xs" c="dimmed">{enabled.length} / {overview.backends.length} enabled · client auth {overview.authEnabled ? 'on' : 'off'}</Text>
        </Group>
      </div>
      <Card className="overview-gateway-card" withBorder p="md">
        {ordered.length === 0 ? <Text size="sm" c="dimmed">No enabled providers.</Text> : (
          <div className="overview-readiness-list">
            {ordered.slice(0, 8).map((backend) => <div key={backend.name} className="overview-readiness-row">
              <Text size="sm" fw={550} className="overview-identifier">{backend.name}</Text>
              <Text size="xs" className="overview-readiness-status" data-attention={needsAttention(backend)}>{!backend.hasKey && !backend.authConfigured ? 'Auth needed' : !backend.catalogOK ? 'Catalog unavailable' : 'Catalog ready'}</Text>
            </div>)}
          </div>
        )}
        <Group gap="sm" mt="sm">
          <Button component={Link} to="/providers" variant="default" size="compact-sm" rightSection={<IconArrowUpRight size={14} />}>Manage providers{ordered.length > 8 ? ` (${enabled.length})` : ''}</Button>
          <Button component={Link} to="/setup" variant="subtle" size="compact-sm">Connect a client</Button>
        </Group>
      </Card>
    </aside>
  )
}
