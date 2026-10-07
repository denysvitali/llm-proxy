import { useMemo, useState } from 'react'
import { Accordion, Badge, Box, Button, Card, Group, SimpleGrid, Skeleton, Stack, Text, Title } from '@mantine/core'
import { IconActivity, IconArrowUpRight, IconBolt, IconCoins, IconInboxOff, IconShieldCheck, IconWallet, IconServer, IconTerminal2 } from '@tabler/icons-react'
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

  return (
    <Fade pending={statsQ.isPending || ovQ.isPending}>
      <Stack gap="lg" className="overview-page">
        <PageHeader title="Overview" subtitle="Traffic, performance, and the health of your gateway."
          extra={<Button component={Link} to="/models" variant="default" size="sm" rightSection={<IconArrowUpRight size={16} />}>Explore models</Button>} />

        {ovQ.isError && <ErrorRetryCard title="Instance details unavailable" message="Configuration and subscription visibility could not be refreshed. Traffic statistics are loaded separately."
          onRetry={() => ovQ.refetch({ cancelRefetch: false })} retrying={ovQ.isFetching} />}

        <PageSection title="Traffic summary" description="All recorded traffic · independent of the chart range">
          {statsQ.data ? <>
            <SimpleGrid cols={{ base: 2, md: 4 }} spacing={0} className="overview-metric-band">
              <StatTile label="Requests" value={fmtInt(totals.requests)} hint={`${fmtInt(totals.successes)} succeeded`} icon={<IconActivity size={17} />} />
              <StatTile label="Success rate" value={totals.requests ? `${(100 * totals.successes / totals.requests).toFixed(1)}%` : '—'}
                hint={totals.requests ? `${fmtInt(totals.requests - totals.successes)} unsuccessful` : 'No recorded attempts'} icon={<IconShieldCheck size={17} />} accent={totals.requests && totals.successes / totals.requests < 0.99 ? 'orange' : undefined} />
              <StatTile label="Tokens served" value={fmtInt(totals.input + totals.output)} hint={`${fmtInt(totals.input)} in · ${fmtInt(totals.output)} out`} icon={<IconCoins size={17} />} />
              <StatTile label="Median tok/s" value={fmtTps(totals.speed)} hint="Median of active model p50s" icon={<IconBolt size={17} />} />
            </SimpleGrid>
            <Group className="overview-summary-footer" justify="space-between" gap="xs">
              <Text size="xs" c="dimmed">{models.filter((model) => model.requests > 0).length} {models.filter((model) => model.requests > 0).length === 1 ? 'model' : 'models'} with traffic</Text>
              <Group gap="md"><Text size="xs" c="dimmed"><Text span inherit fw={600}>{fmtInt(totals.toolCalls)}</Text> tool calls</Text><Text size="xs" style={{ color: totals.toolErrors ? 'var(--data-critical)' : undefined }} c={totals.toolErrors ? undefined : 'dimmed'}><Text span inherit fw={600}>{fmtInt(totals.toolErrors)}</Text> tool errors{totals.toolCalls > 0 ? ` · ${(100 * clampRate(totals.toolErrors / totals.toolCalls)).toFixed(1)}%` : ''}</Text></Group>
            </Group>
          </> : statsQ.isPending ? <SimpleGrid cols={{ base: 2, md: 4 }}>{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} height={122} radius="lg" />)}</SimpleGrid> : null}
          {statsQ.isError ? <ErrorRetryCard title="Couldn't load proxy statistics" message={statsQ.error instanceof Error ? `Model statistics are temporarily unavailable. ${statsQ.error.message}` : 'Model statistics are temporarily unavailable.'}
            onRetry={() => statsQ.refetch({ cancelRefetch: false })} retrying={statsQ.isFetching} /> : models.length === 0 && !statsQ.isPending ? <EmptyState icon={<IconInboxOff size={20} />} title="No model traffic yet" hint="Send a request through the proxy and per-model stats will land here." /> : null}
        </PageSection>

        <div className="overview-monitoring-grid">
          <OverviewHistory query={seriesQ} range={range} onRangeChange={setRange} />
          {ov && <GatewaySnapshot overview={ov} />}
        </div>

        {usageNames.length > 0 && <Accordion variant="separated" radius="lg" className="overview-subscriptions">
          <Accordion.Item value="subscriptions">
            <Accordion.Control icon={<IconWallet size={20} stroke={1.6} />}>
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

        {statsQ.data && <OverviewTraffic models={models} />}

        <PageSection title="Recent activity" description="Retained upstream attempts · updates live and every 30 seconds · independent of chart range">
          <div className="overview-activity-grid">
            <Stack gap="sm">
              {requestsQ.isError && <ErrorRetryCard title="Couldn't refresh recent requests" message={requestsQ.data ? 'Showing the last available attempts; this list may be out of date.' : 'Request history could not be loaded. Retry to check recent attempts.'}
                onRetry={() => requestsQ.refetch({ cancelRefetch: false })} retrying={requestsQ.isFetching} />}
              {(requestsQ.data || requestsQ.isPending) && <RecentRequestsCard requests={requestsQ.data?.requests ?? []} loading={requestsQ.isPending} isMobile={isMobile} />}
            </Stack>
            <Stack gap="sm">
              {errorsQ.isError && <ErrorRetryCard title="Couldn't refresh upstream errors" message={errorsQ.data ? 'Showing the last available errors; this list may be out of date.' : 'The error feed is unavailable. This does not mean there were no failures.'}
                onRetry={() => errorsQ.refetch({ cancelRefetch: false })} retrying={errorsQ.isFetching} />}
              {errorsQ.data ? <UpstreamErrorsCard errors={errorsQ.data.errors ?? []} /> : errorsQ.isPending ? <Skeleton height={180} radius="lg" /> : null}
            </Stack>
          </div>
        </PageSection>
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
      <div className="overview-gateway-heading"><Title order={2} fz={16} fw={650}>Gateway readiness</Title><IconServer size={18} aria-hidden /></div>
      <Card className="overview-gateway-card" p="lg">
        <Text size="xs" c="dimmed">Enabled providers</Text>
        <div className="overview-gateway-total"><Text className="stat-value" fz={36} fw={650} lh={1.2}>{enabled.length}</Text><Text size="sm" c="dimmed">/ {overview.backends.length} configured</Text></div>
        <Badge color={attention.length ? 'yellow' : 'gray'} variant="light" tt="none" mt="sm">{attention.length ? `${attention.length} need attention` : enabled.length ? 'Catalogs ready' : 'No enabled providers'}</Badge>
        <Text size="xs" c="dimmed" mt="md" lh={1.5}>Authentication and catalog availability. Request success is shown in the traffic breakdown.</Text>
        <div className="overview-readiness-list">
          {ordered.slice(0, 4).map((backend) => <div key={backend.name} className="overview-readiness-row">
            <Text size="sm" fw={550} className="overview-identifier">{backend.name}</Text>
            <Text size="xs" className="overview-readiness-status" data-attention={needsAttention(backend)}>{!backend.hasKey && !backend.authConfigured ? 'Auth needed' : !backend.catalogOK ? 'Catalog unavailable' : 'Catalog ready'}</Text>
          </div>)}
        </div>
        <Button component={Link} to="/providers" fullWidth variant="default" size="sm" mt="md" rightSection={<IconArrowUpRight size={15} />}>Manage providers{ordered.length > 4 ? ` (${enabled.length})` : ''}</Button>
        <div className="overview-gateway-auth"><IconShieldCheck size={16} aria-hidden /><Text size="xs">Client auth <Text span inherit fw={600}>{overview.authEnabled ? 'on' : 'off'}</Text></Text></div>
      </Card>
      <Card p="lg" className="overview-connect-card">
        <IconTerminal2 size={20} aria-hidden />
        <Text size="sm" fw={650} mt="sm">One gateway. Every client.</Text>
        <Text size="xs" c="dimmed" lh={1.6} mt={4}>Connect Claude Code, Codex CLI, or any compatible API client.</Text>
        <Button component={Link} to="/setup" variant="light" fullWidth mt="md" size="sm" rightSection={<IconArrowUpRight size={15} />}>Connect a client</Button>
      </Card>
    </aside>
  )
}
