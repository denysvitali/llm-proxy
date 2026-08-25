import type { ReactNode } from 'react'
import {
  Box,
  Badge,
  Card,
  Code,
  Divider,
  Group,
  Loader,
  RingProgress,
  SimpleGrid,
  Stack,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconServerOff } from '@tabler/icons-react'
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
        <div>
          <Title order={4} mb={2}>Providers</Title>
          <Text size="xs" c="dimmed">
            {backends.length} configured · health, token mix, and catalog per provider
          </Text>
        </div>
        {ovQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : backends.length === 0 ? (
          <Stack align="center" py="xl" gap={6}>
            <ThemeIcon variant="light" color="gray" size="lg" radius="xl">
              <IconServerOff size={20} stroke={1.6} />
            </ThemeIcon>
            <Text fw={600}>No providers configured</Text>
            <Text size="sm" c="dimmed" ta="center" maw={340}>
              Add a backend to the proxy config and it will appear here.
            </Text>
          </Stack>
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

// Colored dot + explicit label so key/catalog state is never color-alone;
// same status hues as UptimeBadge.
function StatusDot({
  ok,
  okLabel,
  badLabel,
}: {
  ok: boolean
  okLabel: string
  badLabel: string
}) {
  const color = ok ? '#0ca30c' : '#ec835a'
  return (
    <Group gap={5} wrap="nowrap">
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: '50%',
          background: color,
          display: 'inline-block',
          flexShrink: 0,
        }}
      />
      <Text size="xs" c="dimmed">
        {ok ? okLabel : badLabel}
      </Text>
    </Group>
  )
}

function CardSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Box>
      <Text
        size="xs"
        tt="uppercase"
        c="dimmed"
        fw={600}
        mb={6}
        style={{ letterSpacing: '0.03em' }}
      >
        {title}
      </Text>
      <Box style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>{children}</Box>
    </Box>
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
  const segTotal = segments.reduce((s, x) => s + x.value, 0)
  const shownModels = b.models?.slice(0, 5) ?? []
  const extra = (b.models?.length ?? 0) - shownModels.length

  // Ring color mirrors UptimeBadge thresholds; the badge under the ring
  // carries the icon+label so state is never color-alone.
  const ringColor = !requests ? 'gray' : uptime >= 0.99 ? 'teal' : uptime >= 0.9 ? 'yellow' : 'red'

  return (
    <Card withBorder radius="lg" p="lg">
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="md">
        {/* Left: identity + config health. */}
        <Box style={{ minWidth: 0 }}>
          <Group gap="xs" mb={4}>
            <Title order={5} mb={0}>
              {b.name}
            </Title>
            <Badge size="sm" variant="light" color={b.enabled ? 'teal' : 'gray'}>
              {b.enabled ? 'enabled' : 'disabled'}
            </Badge>
          </Group>
          <Code>{b.host}</Code>
          <Group gap="sm" wrap="wrap" mt={8}>
            <StatusDot
              ok={b.hasKey}
              okLabel="API key set"
              badLabel="API key missing"
            />
            <StatusDot
              ok={b.catalogOK}
              okLabel="catalog ok"
              badLabel="catalog unavailable"
            />
          </Group>
        </Box>
        {/* Right: uptime ring with its state badge stacked under it so the
              pair reads as one unit. */}
        <Stack align="center" gap={4} style={{ flexShrink: 0 }}>
          <Tooltip
            label={
              requests
                ? `${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`
                : 'No requests recorded yet'
            }
            withArrow
          >
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
            />
          </Tooltip>
          <UptimeBadge uptime={uptime} requests={requests} />
        </Stack>
      </Group>

      {/* Stats show whenever traffic exists — even if every request carried
            zero tokens (segments all empty), the counts still matter. */}
      {requests > 0 && (
        <>
          <Divider my="sm" />
          {segTotal > 0 && (
            <>
              <TokenMixBar segments={segments} height={14} />
              <TokenLegend segments={segments} showPercent />
            </>
          )}
          <Text size="xs" c="dimmed" mt={segTotal > 0 ? 6 : 0}>
            {fmtInt(requests)} requests · uptime {fmtPct(uptime)} · tools{' '}
            {fmtInt(toolCalls)} ({fmtPct(toolCalls ? toolErrors / toolCalls : 0)} err)
          </Text>
        </>
      )}

      {routes.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Routes · ${routes.length}`}>
            {routes.map((r) => (
              <Code key={r.model} style={{ fontSize: '0.72rem' }}>
                {r.model} → {r.upstream || '(as requested)'}
              </Code>
            ))}
          </CardSection>
        </>
      )}

      {shownModels.length > 0 && (
        <>
          <Divider my="sm" />
          <CardSection title={`Catalog · ${(b.models?.length ?? 0)}`}>
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
          </CardSection>
        </>
      )}
    </Card>
  )
}
