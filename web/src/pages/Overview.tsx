import { useMemo, useState } from 'react'
import { Badge, Button, Card, Group, Skeleton, Stack, Text } from '@mantine/core'
import {
  IconActivity, IconArrowUpRight, IconBolt, IconCoins, IconInboxOff,
  IconPlugConnected, IconShieldCheck,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { dashboardQueries } from '../queries'
import type { ModelStat, Overview, OverviewBackend } from '../api'
import GrokUsageCard from '../components/GrokUsageCard'
import ZcodeUsageCard from '../components/ZcodeUsageCard'
import MiniMaxUsageCard from '../components/MiniMaxUsageCard'
import OpenDesignUsageCard from '../components/OpenDesignUsageCard'
import { PageHeader } from '../components/PageHeader'
import { PageSection } from '../components/PageSection'
import { clampRate, fmtInt, fmtTps } from '../format'
import { EmptyState } from '../components/EmptyState'
import Fade from '../components/Fade'
import ErrorRetryCard from '../components/ErrorRetryCard'
import OverviewHistory from '../components/overview/OverviewHistory'
import OverviewTraffic from '../components/overview/OverviewTraffic'
import OverviewHeroBand, { HeroStat, OverviewSummaryMeta, RequestSparkline } from '../components/overview/OverviewHero'
import OverviewReveal from '../components/overview/OverviewReveal'
import { OverviewSubscriptions } from '../components/overview/OverviewSubscriptions'
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
  const zcodeQuotaQ = useQuery(dashboardQueries.zcodeQuota(zcodeUsageEnabled))
  const minimaxUsageQ = useQuery(dashboardQueries.minimaxUsage(minimaxUsageEnabled))
  const openDesignUsageEnabled = ov?.openDesignUsage?.configured ?? false
  const openDesignUsageQ = useQuery(dashboardQueries.openDesignUsage(openDesignUsageEnabled))
  const [range, setRange] = useState('24h')
  const seriesQ = useQuery(dashboardQueries.series(range))
  const errorsQ = useQuery(dashboardQueries.errors())
  const requestsQ = useQuery(dashboardQueries.requests())
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
  const modelsWithTraffic = models.filter((model) => model.requests > 0).length
  const successRatio = totals.requests ? totals.successes / totals.requests : Number.NaN
  const successPercent = totals.requests ? `${(100 * clampRate(successRatio)).toFixed(1)}%` : '—'

  const usageNames = [grokUsageEnabled && 'Grok', zcodeUsageEnabled && 'ZCode', minimaxUsageEnabled && 'MiniMax', openDesignUsageEnabled && 'Open Design'].filter((name): name is string => Boolean(name))

  const coreFailed = statsQ.isError && ovQ.isError && seriesQ.isError && errorsQ.isError && requestsQ.isError
  return (
    <Fade pending={statsQ.isPending || ovQ.isPending}>
      <Stack gap="lg" className="overview-page">
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

        <OverviewReveal index={0}>
          <section aria-label="Traffic summary">
            {statsQ.data ? <>
              <OverviewHeroBand>
                <HeroStat
                  primary
                  label="Requests"
                  value={fmtInt(totals.requests)}
                  hint={`${fmtInt(totals.successes)} succeeded`}
                  icon={<IconActivity size={15} />}
                >
                  <RequestSparkline points={seriesQ.data?.series.requests} />
                </HeroStat>
                <HeroStat
                  label="Success rate"
                  value={successPercent}
                  hint={totals.requests ? `${fmtInt(totals.requests - totals.successes)} unsuccessful` : 'No recorded attempts'}
                  icon={<IconShieldCheck size={15} />}
                  accent={totals.requests && successRatio < 0.99 ? 'warning' : undefined}
                />
                <HeroStat
                  label="Tokens served"
                  value={fmtInt(totals.input + totals.output)}
                  hint={`${fmtInt(totals.input)} in · ${fmtInt(totals.output)} out`}
                  icon={<IconCoins size={15} />}
                />
                <HeroStat
                  label="Median tok/s"
                  value={fmtTps(totals.speed)}
                  hint="Median of active model p50s"
                  icon={<IconBolt size={15} />}
                />
              </OverviewHeroBand>
              <OverviewSummaryMeta modelsWithTraffic={modelsWithTraffic} toolCalls={totals.toolCalls} toolErrors={totals.toolErrors} />
            </> : statsQ.isPending ? (
              <OverviewHeroBand>
                {Array.from({ length: 4 }, (_, i) => (
                  <div key={i} className="stat-tile overview-hero-tile" aria-hidden>
                    <Skeleton height={11} width={64} radius="sm" />
                    <Skeleton height={28} width={96} radius="sm" mt={12} />
                    <Skeleton height={12} width={72} radius="sm" mt={10} />
                  </div>
                ))}
              </OverviewHeroBand>
            ) : null}
            {!coreFailed && statsQ.isError ? (
              <ErrorRetryCard title="Couldn't load proxy statistics" message={statsQ.error instanceof Error ? `Model statistics are temporarily unavailable. ${statsQ.error.message}` : 'Model statistics are temporarily unavailable.'}
                onRetry={() => statsQ.refetch({ cancelRefetch: false })} retrying={statsQ.isFetching} />
            ) : !coreFailed && statsQ.data && models.length === 0 ? (
              <EmptyState icon={<IconInboxOff size={20} />} title="No model traffic yet" hint="Send a request through the proxy and per-model stats will land here." />
            ) : null}
          </section>
        </OverviewReveal>

        {!coreFailed && <OverviewReveal index={1}><OverviewHistory query={seriesQ} range={range} onRangeChange={setRange} /></OverviewReveal>}

        {!coreFailed && usageNames.length > 0 && (
          <OverviewReveal index={2}>
            <OverviewSubscriptions accounts={usageNames}>
              {grokUsageEnabled && <GrokUsageCard query={grokUsageQ} />}
              {zcodeUsageEnabled && <ZcodeUsageCard query={zcodeQuotaQ} />}
              {minimaxUsageEnabled && <MiniMaxUsageCard query={minimaxUsageQ} />}
              {openDesignUsageEnabled && <OpenDesignUsageCard query={openDesignUsageQ} />}
            </OverviewSubscriptions>
          </OverviewReveal>
        )}

        {!coreFailed && (
          <OverviewReveal index={3}>
            <PageSection title="Recent activity" description="Retained upstream attempts and failures · updates live and every 30 seconds · independent of chart range">
              <div className="overview-activity-grid">
                <Stack gap="sm" miw={0}>
                  {requestsQ.isError && <ErrorRetryCard title="Couldn't refresh recent requests" message={requestsQ.data ? 'Showing the last available attempts; this list may be out of date.' : 'Request history could not be loaded. Retry to check recent attempts.'}
                    onRetry={() => requestsQ.refetch({ cancelRefetch: false })} retrying={requestsQ.isFetching} />}
                  {(requestsQ.data || requestsQ.isPending) && <RecentRequestsCard requests={requestsQ.data?.requests ?? []} loading={requestsQ.isPending} />}
                </Stack>
                <Stack gap="sm" miw={0}>
                  {errorsQ.isError && <ErrorRetryCard title="Couldn't refresh upstream errors" message={errorsQ.data ? 'Showing the last available errors; this list may be out of date.' : 'The error feed is unavailable. This does not mean there were no failures.'}
                    onRetry={() => errorsQ.refetch({ cancelRefetch: false })} retrying={errorsQ.isFetching} />}
                  {errorsQ.data ? <UpstreamErrorsCard errors={errorsQ.data.errors ?? []} /> : errorsQ.isPending ? <Skeleton height={352} radius="lg" /> : null}
                </Stack>
              </div>
            </PageSection>
          </OverviewReveal>
        )}

        {!coreFailed && ov && <OverviewReveal index={4}><GatewayReadiness overview={ov} /></OverviewReveal>}
        {!coreFailed && statsQ.data && <OverviewReveal index={5}><OverviewTraffic models={models} /></OverviewReveal>}
      </Stack>
    </Fade>
  )
}

// Gateway readiness as a compact status matrix: one row per enabled provider,
// ordered attention-first, under a count summary. Every row names its state in
// text beside the dot, so color is never the only signal.
function GatewayReadiness({ overview }: { overview: Overview }) {
  const enabled = (overview.backends ?? []).filter((backend): backend is OverviewBackend => Boolean(backend?.enabled))
  const needsAttention = (backend: OverviewBackend) => (!backend.hasKey && !backend.authConfigured) || !backend.catalogOK
  const attention = enabled.filter(needsAttention)
  const ordered = [...attention, ...enabled.filter((backend) => !needsAttention(backend))]
  const total = (overview.backends ?? []).filter(Boolean).length

  return (
    <PageSection title="Gateway readiness" description="Configuration state of every enabled provider">
      <Card p="md" miw={0} className="overview-readiness-card">
        <div className="overview-readiness-summary">
          <Group gap={6} wrap="wrap">
            <Badge variant="light" color="gray" tt="none" size="sm">{enabled.length} of {total} enabled</Badge>
            <Badge variant="light" color="gray" tt="none" size="sm">client auth {overview.authEnabled ? 'on' : 'off'}</Badge>
            <Badge
              variant="light"
              tt="none"
              size="sm"
              color={attention.length ? 'yellow' : 'gray'}
              styles={attention.length ? { root: { color: 'var(--data-warning)', background: 'color-mix(in srgb, var(--data-warning) 12%, var(--card))' } } : undefined}
            >
              {attention.length ? `${attention.length} need attention` : enabled.length ? 'All catalogs ready' : 'No enabled providers'}
            </Badge>
          </Group>
          <Button component={Link} to="/providers" variant="default" size="compact-sm" rightSection={<IconArrowUpRight size={14} />}>
            Manage providers{total > enabled.length ? ` (${total})` : ''}
          </Button>
        </div>

        {ordered.length === 0 ? (
          <Text size="sm" c="dimmed">No enabled providers.</Text>
        ) : (
          <div className="overview-readiness-grid" role="list" aria-label="Enabled providers">
            {ordered.map((backend) => {
              const state = !backend.hasKey && !backend.authConfigured
                ? { key: 'auth', label: 'Auth needed', tone: 'var(--data-warning)' }
                : !backend.catalogOK
                  ? { key: 'catalog', label: 'Catalog unavailable', tone: 'var(--data-critical)' }
                  : { key: 'ready', label: 'Catalog ready', tone: 'var(--data-good)' }
              return (
                <Link key={backend.name} to="/providers" className="overview-readiness-row" role="listitem" aria-label={`${backend.name}: ${state.label}`}>
                  <span className="overview-state-dot" aria-hidden="true" style={{ background: state.tone }} data-state={state.key} />
                  <span className="overview-ident overview-readiness-name">{backend.name}</span>
                  <span className="overview-readiness-state" data-state={state.key}>{state.label}</span>
                  <IconArrowUpRight aria-hidden="true" size={13} className="overview-readiness-chevron" />
                </Link>
              )
            })}
          </div>
        )}

        <Group gap="sm" mt="sm" wrap="wrap">
          <Text size="xs" c="dimmed">Providers are configured in the proxy config file; this dashboard only reports state.</Text>
          <Button component={Link} to="/setup" variant="subtle" size="compact-xs" leftSection={<IconPlugConnected size={14} />}>Connect a client</Button>
        </Group>
      </Card>
    </PageSection>
  )
}
