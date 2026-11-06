import type { UseQueryResult } from '@tanstack/react-query'
import { Badge, Box, Button, Card, Group, Text, Tooltip } from '@mantine/core'
import { IconChevronRight } from '@tabler/icons-react'
import type { GrokUsage, MiniMaxUsage, OpenDesignUsage, OverviewBackend } from '../../api'
import { fmtInt, plural } from '../../format'
import UptimeBadge from '../UptimeBadge'
import { MiniMaxUsageCompact } from '../MiniMaxUsageCard'
import { OpenDesignUsageCompact } from '../OpenDesignUsageCard'
import TokenMixBar from '../TokenMixBar'
import UsageMeter from '../UsageMeter'
import { periodTypeLabel, usagePercent } from '../usageFormatting'
import { pct, type ProviderAggregate, type StatsState } from './providerData'
import { StatusDot } from './ProviderStatus'

// One provider, one card, one fixed anatomy: identity header (name + host +
// model count on one meta line), single status row, 3-up metric strip, token
// mix (only when there is token data), an optional one-line account usage
// strip, and a full-width footer action. The card is a flex column with the
// footer pushed to the bottom, and the grid stretches every card in a row to
// the row's height — so metrics and actions always line up, even when a
// provider carries a subscription strip and its neighbour doesn't.
export default function ProviderCard({
  backend,
  aggregate,
  segments,
  routeCount,
  statsState,
  grokUsage,
  minimaxUsageQuery,
  openDesignUsageQuery,
  onInspect,
}: {
  backend: OverviewBackend
  aggregate: ProviderAggregate
  segments: Parameters<typeof TokenMixBar>[0]['segments']
  routeCount: number
  statsState: StatsState
  grokUsage?: GrokUsage
  minimaxUsageQuery?: UseQueryResult<MiniMaxUsage, Error>
  openDesignUsageQuery?: UseQueryResult<OpenDesignUsage, Error>
  onInspect: () => void
}) {
  const ready = statsState === 'ready'
  const tokenTotal = segments.reduce((sum, segment) => sum + segment.value, 0)
  const hasAccount = ['grok', 'workbuddy', 'codex', 'zcode', 'minimax-code'].includes(backend.name)
  const authenticated = hasAccount ? backend.authConfigured : backend.hasKey
  const initials = backend.name.slice(0, 2).toUpperCase()
  const modelCount = backend.models?.length ?? 0
  const host = backend.host?.trim() || 'default endpoint'
  const catalogLine = backend.catalogOK ? plural(modelCount, 'model') : 'Models unavailable'
  const metaLine = routeCount > 0
    ? `${catalogLine} · ${routeCount} route${routeCount === 1 ? '' : 's'}`
    : catalogLine

  return (
    <Card withBorder radius="lg" p={0} className="provider-card catalog-provider-card" miw={0}>
      <div className="catalog-provider-main">
        <div className="catalog-provider-head">
          <span className="catalog-provider-avatar" aria-hidden>
            {initials}
          </span>
          <Box miw={0} className="catalog-provider-identity">
            <Group gap={6} wrap="nowrap" className="catalog-provider-name-row">
              <Text className="catalog-provider-name" title={backend.name}>
                {backend.name}
              </Text>
              {!backend.enabled && (
                <Badge size="xs" variant="light" color="gray" className="catalog-disabled-badge">
                  Disabled
                </Badge>
              )}
            </Group>
            <Tooltip label={`${host} · ${metaLine}`} withArrow openDelay={500}>
              <Text size="xs" c="dimmed" className="catalog-provider-host">
                {host}
                <span className="catalog-provider-host-sep"> · </span>
                <span className="catalog-provider-host-meta">{metaLine}</span>
              </Text>
            </Tooltip>
          </Box>
        </div>

        {/* One line of state. Problems are named; a provider that is fine
            says so once instead of repeating two "ready" chips. Text always
            accompanies the dot, so status is never color alone. */}
        <div className="catalog-provider-statusline">
          <UptimeBadge uptime={ready ? aggregate.uptime : NaN} requests={ready ? aggregate.requests : NaN} />
          {backend.catalogOK ? null : (
            <StatusDot ok={false} okLabel="Catalog ready" badLabel="No catalog" />
          )}
          {authenticated ? null : (
            <StatusDot
              ok={false}
              okLabel={hasAccount ? 'Account connected' : 'Key configured'}
              badLabel={hasAccount ? 'Sign-in needed' : 'Key missing'}
            />
          )}
          {backend.catalogOK && authenticated ? (
            <span className="catalog-status-ready">
              <span className="catalog-status-dot" data-ok="true" aria-hidden="true" />
              Ready
            </span>
          ) : null}
        </div>

        <div className="catalog-provider-metrics">
          <Metric label="Requests" value={ready ? fmtInt(aggregate.requests) : '—'} />
          <Metric
            label="Success"
            value={ready && aggregate.requests > 0 ? pct(aggregate.uptime) : '—'}
            tone={
              ready && aggregate.requests > 0
                ? aggregate.uptime >= 0.9
                  ? undefined
                  : aggregate.uptime >= 0.5
                    ? 'warning'
                    : 'critical'
                : undefined
            }
          />
          <Metric label="Tokens" value={ready ? fmtInt(tokenTotal) : '—'} />
        </div>
        {tokenTotal > 0 && (
          <div className="catalog-provider-mix">
            <TokenMixBar segments={segments} height={5} />
          </div>
        )}

        {(grokUsage || minimaxUsageQuery || openDesignUsageQuery) && (
          <div className="catalog-provider-usage">
            {grokUsage && <GrokUsageLine usage={grokUsage} />}
            {minimaxUsageQuery &&
              (minimaxUsageQuery.data ? (
                <MiniMaxUsageCompact usage={minimaxUsageQuery.data} />
              ) : (
                <Text size="xs" c="dimmed">
                  {minimaxUsageQuery.isPending ? 'Loading MiniMax account…' : 'Usage unavailable'}
                </Text>
              ))}
            {openDesignUsageQuery &&
              (openDesignUsageQuery.data ? (
                <OpenDesignUsageCompact usage={openDesignUsageQuery.data} />
              ) : (
                <Text size="xs" c="dimmed">
                  {openDesignUsageQuery.isPending ? 'Loading Open Design plan…' : 'Usage unavailable'}
                </Text>
              ))}
          </div>
        )}

        <div className="catalog-provider-footer">
          <Button
            aria-label={`Inspect ${backend.name}`}
            onClick={onInspect}
            variant="subtle"
            color="gray"
            className="catalog-provider-inspect"
            rightSection={<IconChevronRight size={15} aria-hidden />}
          >
            Details
          </Button>
        </div>
      </div>
    </Card>
  )
}

// One line of subscription usage: tier + period, percentage, hairline meter.
// The date range lives in the provider drawer, not in a grid card.
function GrokUsageLine({ usage }: { usage: GrokUsage }) {
  const percent = usagePercent(usage)
  const period = periodTypeLabel(usage.periodType)
  return (
    <div className="catalog-usage-line">
      <div className="catalog-usage-line-head">
        <Text size="xs" c="dimmed" className="catalog-usage-line-label">
          {usage.subscriptionTier || 'Grok'}
          {period ? ` · ${period}` : ''}
        </Text>
        <Text size="xs" fw={600} className="tabular">
          {percent === null ? 'unavailable' : `${percent.toFixed(1)}% used`}
        </Text>
      </div>
      {percent !== null && (
        <UsageMeter percent={percent} compact label={`${usage.subscriptionTier || 'Grok'} subscription quota used`} />
      )}
    </div>
  )
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: 'warning' | 'critical' }) {
  return (
    <Box miw={0} className="catalog-provider-metric">
      <Text className="micro-label catalog-provider-metric-label">{label}</Text>
      <Text className="tabular catalog-provider-metric-value" data-tone={tone}>
        {value}
      </Text>
    </Box>
  )
}
