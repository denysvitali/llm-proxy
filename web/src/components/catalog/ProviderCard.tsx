import { Badge, Box, Button, Card, Group, Stack, Text, Title } from '@mantine/core'
import { IconChevronRight, IconKey, IconUser } from '@tabler/icons-react'
import type { UseQueryResult } from '@tanstack/react-query'
import type { GrokUsage, MiniMaxUsage, ModelStat, OverviewBackend } from '../../api'
import { fmtInt } from '../../format'
import UptimeBadge from '../UptimeBadge'
import GrokUsageCompact from '../GrokUsageCompact'
import { MiniMaxUsageCompact } from '../MiniMaxUsageCard'
import TokenMixBar from '../TokenMixBar'
import { backendAgg, pct, type StatsState } from './providerData'
import { StatusDot } from './ProviderStatus'

export default function ProviderCard({
  backend,
  models,
  segments,
  routeCount,
  statsState,
  grokUsage,
  minimaxUsageQuery,
  onInspect,
}: {
  backend: OverviewBackend
  models: ModelStat[]
  segments: Parameters<typeof TokenMixBar>[0]['segments']
  routeCount: number
  statsState: StatsState
  grokUsage?: GrokUsage
  minimaxUsageQuery?: UseQueryResult<MiniMaxUsage, Error>
  onInspect: () => void
}) {
  const aggregate = backendAgg(models, backend.name)
  const ready = statsState === 'ready'
  const tokenTotal = segments.reduce((sum, segment) => sum + segment.value, 0)
  const hasAccount = ['grok', 'workbuddy', 'codex', 'zcode', 'minimax-code'].includes(backend.name)
  const authenticated = hasAccount ? backend.authConfigured : backend.hasKey
  return (
    <Card withBorder radius="lg" p={0} className="provider-card catalog-provider-card" miw={0}>
      <div className="catalog-provider-main">
        <div className="catalog-provider-topline">
          <Group gap="sm" wrap="nowrap" align="flex-start" className="catalog-provider-identity">
            <span className="catalog-provider-avatar" aria-hidden>
              {backend.name.slice(0, 2).toUpperCase()}
            </span>
            <Box miw={0}>
              <Group gap="xs">
                <Title order={3} size="h4" className="catalog-model-name">{backend.name}</Title>
                {!backend.enabled && <Badge size="xs" variant="light" color="gray">Disabled</Badge>}
              </Group>
              <Text size="xs" c="dimmed" className="catalog-provider-host" mt={3}>{backend.host}</Text>
              <Text size="xs" c="dimmed" mt={4}>
                {backend.catalogOK ? `${fmtInt(backend.models?.length ?? 0)} models` : 'Models unavailable'}
                {routeCount > 0 && ` · ${routeCount} explicit route${routeCount === 1 ? '' : 's'}`}
              </Text>
            </Box>
          </Group>
          <Stack gap={7} align="flex-end" className="catalog-provider-status">
            <Box><UptimeBadge uptime={ready ? aggregate.uptime : NaN} requests={ready ? aggregate.requests : NaN} /></Box>
            <Group gap="sm" wrap="wrap" justify="flex-end" className="catalog-provider-state">
              <StatusDot ok={backend.catalogOK} okLabel="Catalog ready" badLabel="Catalog unavailable" />
              <Group gap={5} wrap="nowrap" style={{ color: authenticated ? undefined : 'var(--data-warning)' }} c={authenticated ? 'dimmed' : undefined}>
                {hasAccount ? <IconUser size={13} aria-hidden /> : <IconKey size={13} aria-hidden />}
                <Text size="xs">{authenticated ? (hasAccount ? 'Account connected' : 'Key configured') : 'Auth needed'}</Text>
              </Group>
            </Group>
          </Stack>
        </div>
        <div className="catalog-provider-metrics">
          <Metric label="Requests" value={ready ? fmtInt(aggregate.requests) : '—'} />
          <Metric label="Success" value={ready && aggregate.requests > 0 ? pct(aggregate.uptime) : '—'} />
          <Metric label="Tokens" value={ready ? fmtInt(tokenTotal) : '—'} />
        </div>
        <Button
          aria-label={`Inspect ${backend.name}`}
          onClick={onInspect}
          variant="light"
          color="brand"
          size="xs"
          mih={44}
          px="xs"
          className="catalog-provider-inspect"
          rightSection={<IconChevronRight size={16} />}
        >
          Details
        </Button>
      </div>
      {(grokUsage || minimaxUsageQuery) && (
        <Stack gap="xs" className="catalog-provider-usage">
          {grokUsage && <GrokUsageCompact usage={grokUsage} />}
          {minimaxUsageQuery &&
            (minimaxUsageQuery.data ? (
              <MiniMaxUsageCompact usage={minimaxUsageQuery.data} />
            ) : (
              <Text size="xs" c="dimmed">
                {minimaxUsageQuery.isPending ? 'Loading MiniMax account…' : 'Usage unavailable'}
              </Text>
            ))}
        </Stack>
      )}
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box miw={0}>
      <Text size="xs" c="dimmed">{label}</Text>
      <Text fw={650} size="sm" mt={5} className="tabular">{value}</Text>
    </Box>
  )
}
