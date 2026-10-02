import { Badge, Box, Button, Card, Group, Stack, Text, Title } from '@mantine/core'
import { IconArrowUpRight, IconChevronRight, IconKey, IconPlugConnected, IconUser } from '@tabler/icons-react'
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
        <Group justify="space-between" wrap="nowrap" align="flex-start" mb="md">
          <Group gap="sm" wrap="nowrap" miw={0}>
            <span className="catalog-provider-avatar" aria-hidden>
              {backend.name.slice(0, 2).toUpperCase()}
            </span>
            <Box miw={0}>
              <Title order={3} size="h4" className="catalog-model-name">
                {backend.name}
              </Title>
              <Group gap={4} mt={4}>
                <IconPlugConnected size={12} aria-hidden />
                <Text size="xs" c="dimmed">
                  {backend.enabled ? 'Enabled' : 'Disabled'}
                </Text>
              </Group>
            </Box>
          </Group>
          <UptimeBadge uptime={ready ? aggregate.uptime : NaN} requests={ready ? aggregate.requests : NaN} />
        </Group>
        <Text size="xs" c="dimmed" className="catalog-provider-host">
          {backend.host}
        </Text>
        <div className="catalog-provider-metrics">
          <Metric label="Requests" value={ready ? fmtInt(aggregate.requests) : '—'} />
          <Metric label="Success" value={ready && aggregate.requests > 0 ? pct(aggregate.uptime) : '—'} />
          <Metric label="Models" value={backend.catalogOK ? fmtInt(backend.models?.length ?? 0) : '—'} />
        </div>
        <Group justify="space-between" gap="xs">
          <StatusDot ok={backend.catalogOK} okLabel="Catalog ready" badLabel="Catalog unavailable" />
          <Badge
            size="sm"
            variant="light"
            color={authenticated ? 'gray' : 'yellow'}
            leftSection={hasAccount ? <IconUser size={11} /> : <IconKey size={11} />}
          >
            {authenticated ? (hasAccount ? 'Account connected' : 'Key configured') : 'Auth needed'}
          </Badge>
        </Group>
        {ready && tokenTotal > 0 && (
          <Box mt="md">
            <TokenMixBar segments={segments} height={5} />
            <Text size="xs" c="dimmed" mt={5}>
              {fmtInt(tokenTotal)} tokens · {fmtInt(aggregate.toolCalls)} tool calls
            </Text>
          </Box>
        )}
        {(grokUsage || minimaxUsageQuery) && (
          <Stack gap="xs" className="catalog-provider-usage" mt="sm">
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
      </div>
      <Group justify="space-between" className="catalog-provider-footer" gap="xs">
        <Text size="xs" c="dimmed">
          {routeCount ? `${routeCount} explicit route${routeCount === 1 ? '' : 's'}` : 'Provider catalog'}
          <IconArrowUpRight size={12} aria-hidden style={{ verticalAlign: 'middle', marginLeft: 4 }} />
        </Text>
        <Button
          aria-label={`Inspect ${backend.name}`}
          onClick={onInspect}
          variant="subtle"
          size="xs"
          mih={44}
          px="xs"
          rightSection={<IconChevronRight size={14} />}
        >
          Inspect provider
        </Button>
      </Group>
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box miw={0}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={650} fz={20} mt={2} className="tabular">
        {value}
      </Text>
    </Box>
  )
}
