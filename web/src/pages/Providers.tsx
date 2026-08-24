import {
  Badge,
  Box,
  Card,
  Code,
  Divider,
  Group,
  Loader,
  RingProgress,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { fetchOverview, fetchStats } from '../api'
import type { ModelStat, OverviewBackend } from '../api'
import { fmtInt, fmtPct } from '../format'
import { useChartPalette } from '../palette'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend } from '../components/TokenMixBar'
import { providerSegments } from './Overview'
import { Fade } from '../App'

export default function ProvidersPage() {
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const pal = useChartPalette()

  const backends = ovQ.data?.backends ?? []
  const models = statsQ.data?.models ?? []
  const segByBackend = new Map(providerSegments(models, pal.series))

  return (
    <Fade fetching={ovQ.isFetching || statsQ.isFetching}>
      <Stack gap="md">
        <Title order={4}>Providers</Title>
        {ovQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : backends.length === 0 ? (
          <Text c="dimmed" py="xl" ta="center">
            No backends configured.
          </Text>
        ) : (
          <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
            {backends.map((b) => (
              <ProviderCard
                key={b.name}
                backend={b}
                routes={(ovQ.data?.routes ?? []).filter((r) => r.backend === b.name)}
                segments={segByBackend.get(b.name) ?? []}
                models={models.filter((m) => m.backend === b.name)}
              />
            ))}
          </SimpleGrid>
        )}
      </Stack>
    </Fade>
  )
}

function ProviderCard({
  backend: b,
  routes,
  segments,
  models,
}: {
  backend: OverviewBackend
  routes: { model: string; backend: string; upstream: string }[]
  segments: Parameters<typeof TokenMixBar>[0]['segments']
  models: ModelStat[]
}) {
  const requests = models.reduce((s, m) => s + m.requests, 0)
  const successes = models.reduce((s, m) => s + m.successes, 0)
  const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
  const uptime = requests ? successes / requests : 0
  const shownModels = b.models?.slice(0, 5) ?? []
  const extra = (b.models?.length ?? 0) - shownModels.length

  // Ring color mirrors UptimeBadge thresholds; the badge next to it carries
  // the icon+label so state is never color-alone.
  const ringColor = !requests ? 'gray' : uptime >= 0.99 ? 'teal' : uptime >= 0.9 ? 'yellow' : 'red'

  return (
    <Card withBorder radius="lg" p="lg">
      <Group justify="space-between" wrap="nowrap" align="flex-start" mb="xs">
        <Box style={{ minWidth: 0 }}>
          <Group gap="xs" mb={4}>
            <Title order={5} mb={0}>
              {b.name}
            </Title>
            <Badge size="sm" variant="light" color={b.enabled ? 'teal' : 'gray'}>
              {b.enabled ? 'enabled' : 'disabled'}
            </Badge>
          </Group>
          <Text size="sm" c="dimmed">
            <Code>{b.host}</Code> · API key {b.hasKey ? 'set' : 'missing'} · catalog{' '}
            {b.catalogOK ? 'ok' : 'unavailable'}
          </Text>
          <Box mt={8}>
            <UptimeBadge uptime={uptime} requests={requests} />
          </Box>
        </Box>
        <RingProgress
          size={84}
          thickness={7}
          roundCaps
          sections={[{ value: requests ? uptime * 100 : 0, color: ringColor }]}
          label={
            <Text ta="center" size="xs" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
              {requests ? fmtPct(uptime, 0) : '—'}
            </Text>
          }
          aria-label={`uptime ${fmtPct(uptime)}`}
          style={{ flexShrink: 0 }}
        />
      </Group>

      {(segments.length > 0 && requests > 0) && (
        <>
          <Divider my="sm" />
          <TokenMixBar segments={segments} height={14} />
          <TokenLegend segments={segments} />
          <Text size="xs" c="dimmed" mt={6}>
            {fmtInt(requests)} requests · uptime {fmtPct(uptime)} · tools{' '}
            {fmtInt(toolCalls)} ({fmtPct(toolCalls ? toolErrors / toolCalls : 0)} err)
          </Text>
        </>
      )}

      {routes.length > 0 && (
        <>
          <Divider my="sm" />
          <Box style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {routes.map((r) => (
              <Code key={r.model} style={{ fontSize: '0.72rem' }}>
                {r.model} → {r.upstream || '(as requested)'}
              </Code>
            ))}
          </Box>
        </>
      )}

      {shownModels.length > 0 && (
        <>
          <Divider my="sm" />
          <Box style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {shownModels.map((m) => (
              <Code key={m} style={{ fontSize: '0.72rem' }}>
                {m}
              </Code>
            ))}
            {extra > 0 && (
              <Text size="xs" c="dimmed" style={{ alignSelf: 'center' }}>
                +{extra} more
              </Text>
            )}
          </Box>
        </>
      )}
    </Card>
  )
}
