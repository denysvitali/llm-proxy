import {
  Box,
  Card,
  Group,
  Loader,
  ScrollArea,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import {
  IconActivity,
  IconAlertTriangle,
  IconBolt,
  IconCoins,
  IconShieldCheck,
  IconTool,
} from '@tabler/icons-react'
import { useMediaQuery } from '@mantine/hooks'
import { useQuery } from '@tanstack/react-query'
import { fetchOverview, fetchStats } from '../api'
import type { ModelStat } from '../api'
import { fmtInt, fmtPct, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import StatTile from '../components/StatTile'
import UptimeBadge from '../components/UptimeBadge'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { Fade } from '../App'

export default function OverviewPage() {
  const statsQ = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  const ovQ = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })

  const models = statsQ.data?.models ?? []
  const pal = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  const totalRequests = models.reduce((s, m) => s + m.requests, 0)
  const totalSuccess = models.reduce((s, m) => s + m.successes, 0)
  const tokensIn = models.reduce((s, m) => s + m.input_tokens, 0)
  const tokensOut = models.reduce((s, m) => s + m.output_tokens, 0)
  const toolErrors = models.reduce((s, m) => s + m.tool_errors, 0)
  const toolCalls = models.reduce((s, m) => s + m.tool_calls, 0)

  // Requests-weighted median throughput across models: the honest aggregate
  // of per-model p50s we can compute without the raw histogram buckets.
  const busy = models.filter((m) => m.throughput_tps.p50 > 0)
  let medianTps = 0
  if (busy.length > 0) {
    const sorted = [...busy].sort(
      (a, b) => a.throughput_tps.p50 - b.throughput_tps.p50,
    )
    medianTps = sorted[Math.floor(sorted.length / 2)].throughput_tps.p50
  }

  return (
    <Fade fetching={statsQ.isFetching || ovQ.isFetching}>
      <Stack gap="lg">
        <Title order={4} mb={-6}>
          Fleet overview
        </Title>

        {statsQ.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : models.length === 0 ? (
          <Text c="dimmed" py="xl" ta="center">
            No model traffic recorded yet — send a request through the proxy and it will show up here.
          </Text>
        ) : (
          <>
            <SimpleGrid cols={{ base: 2, sm: 3, lg: 6 }} spacing="md">
              <StatTile
                label="Requests"
                value={fmtInt(totalRequests)}
                hint={`${fmtInt(totalSuccess)} succeeded`}
                icon={<IconActivity size={16} />}
                accent="brand"
              />
              <StatTile
                label="Uptime"
                value={fmtPct(totalRequests ? totalSuccess / totalRequests : 0)}
                hint="succeeded / total"
                icon={<IconShieldCheck size={16} />}
                accent="teal"
              />
              <StatTile
                label="Tokens served"
                value={fmtInt(tokensIn + tokensOut)}
                hint={`${fmtInt(tokensIn)} in · ${fmtInt(tokensOut)} out`}
                icon={<IconCoins size={16} />}
                accent="grape"
              />
              <StatTile
                label="Median tok/s"
                value={fmtTps(medianTps)}
                hint="p50 across models"
                icon={<IconBolt size={16} />}
                accent="orange"
              />
              <StatTile
                label="Tool calls"
                value={fmtInt(toolCalls)}
                hint={`${fmtInt(toolErrors)} errored`}
                icon={<IconTool size={16} />}
                accent="brand"
              />
              <StatTile
                label="Tool error rate"
                value={fmtPct(toolCalls ? toolErrors / toolCalls : 0)}
                hint="errored / calls"
                icon={<IconAlertTriangle size={16} />}
                accent={toolErrors > 0 ? 'red' : 'gray'}
              />
            </SimpleGrid>

            <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="lg">
              <Card withBorder radius="lg" p="md">
                <Title order={5} mb="sm">
                  Requests by model
                </Title>
                <Stack gap="xs">
                  {[...models]
                    .sort((a, b) => b.requests - a.requests)
                    .slice(0, 8)
                    .map((m) => (
                      <MagnitudeRow key={`${m.backend}/${m.model}`} stat={m} max={totalRequests} color={pal.magnitude} />
                    ))}
                </Stack>
              </Card>

              <Card withBorder radius="lg" p="md">
                <Title order={5} mb="sm">
                  Token mix by provider
                </Title>
                <Stack gap="md">
                  {providerSegments(models, pal.series).map(([backend, segs]) => (
                    <Box key={backend}>
                      <Group justify="space-between" mb={4}>
                        <Text size="sm">{backend}</Text>
                      </Group>
                      <TokenMixBar segments={segs} />
                      <TokenLegend segments={segs} />
                    </Box>
                  ))}
                </Stack>
              </Card>
            </SimpleGrid>

            <Card withBorder radius="lg" p="md">
              <Title order={5} mb="sm">
                Provider uptime
              </Title>
              <ScrollArea>
              <Table verticalSpacing="xs" horizontalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Backend</Table.Th>
                    <Table.Th>Status</Table.Th>
                    <Table.Th ta="right">Uptime</Table.Th>
                    {!isMobile && <Table.Th ta="right">Requests</Table.Th>}
                    {!isMobile && <Table.Th ta="right">Tool err</Table.Th>}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {providerAggregates(models).map((p) => (
                    <Table.Tr key={p.backend}>
                      <Table.Td>{p.backend}</Table.Td>
                      <Table.Td>
                        <UptimeBadge uptime={p.uptime} requests={p.requests} />
                      </Table.Td>
                      <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                        {fmtPct(p.uptime)}
                      </Table.Td>
                      {!isMobile && (
                        <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                          {fmtInt(p.requests)}
                        </Table.Td>
                      )}
                      {!isMobile && (
                        <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }}>
                          {p.toolCalls ? fmtPct(p.toolErrors / p.toolCalls) : '—'}
                        </Table.Td>
                      )}
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
              </ScrollArea>
            </Card>
          </>
        )}
        {ovQ.data && (
          <Text size="xs" c="dimmed">
            {ovQ.data.name} v{ovQ.data.version} · listening on {ovQ.data.listen} · auth{' '}
            {ovQ.data.authEnabled ? 'enabled' : 'disabled'}
          </Text>
        )}
      </Stack>
    </Fade>
  )
}

// Single-hue magnitude row: model label, bar anchored to a common baseline,
// exact count printed beside it.
function MagnitudeRow({
  stat,
  max,
  color,
}: {
  stat: ModelStat
  max: number
  color: string
}) {
  const w = stat.requests > 0 && max > 0 ? Math.max((stat.requests / max) * 100, 1.5) : 0
  return (
    <Box style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
      <Text size="sm" truncate style={{ width: 'clamp(90px, 34%, 200px)', flexShrink: 0 }}>
        {stat.model}
      </Text>
      <Box
        style={{
          flex: 1,
          height: 18,
          background: 'var(--mantine-color-default-border)',
          borderRadius: 4,
        }}
        title={`${stat.backend}/${stat.model}: ${stat.requests} requests`}
      >
        {w > 0 && (
          <div
            style={{
              width: `${w}%`,
              height: '100%',
              background: color,
              borderRadius: '0 4px 4px 0',
            }}
          />
        )}
      </Box>
      <Text size="sm" ta="right" w={64} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {fmtInt(stat.requests)}
      </Text>
    </Box>
  )
}

export function providerSegments(
  models: ModelStat[],
  colors: string[],
): [string, MixSegment[]][] {
  const kinds = [
    (m: ModelStat) => m.input_tokens,
    (m: ModelStat) => m.output_tokens,
    (m: ModelStat) => m.cache_read_tokens,
    (m: ModelStat) => m.cache_write_tokens,
  ]
  const names = ['input', 'output', 'cache read', 'cache write']
  const byBackend = new Map<string, ModelStat[]>()
  for (const m of models) {
    byBackend.set(m.backend, [...(byBackend.get(m.backend) ?? []), m])
  }
  return [...byBackend.entries()].map(([backend, ms]) => [
    backend,
    kinds.map((get, i) => ({
      name: names[i],
      color: colors[i],
      value: ms.reduce((s, m) => s + get(m), 0),
    })),
  ])
}

function providerAggregates(models: ModelStat[]) {
  const byBackend = new Map<
    string,
    { requests: number; successes: number; toolCalls: number; toolErrors: number }
  >()
  for (const m of models) {
    const cur = byBackend.get(m.backend) ?? { requests: 0, successes: 0, toolCalls: 0, toolErrors: 0 }
    cur.requests += m.requests
    cur.successes += m.successes
    cur.toolCalls += m.tool_calls
    cur.toolErrors += m.tool_errors
    byBackend.set(m.backend, cur)
  }
  return [...byBackend.entries()].map(([backend, v]) => ({
    backend,
    requests: v.requests,
    uptime: v.requests ? v.successes / v.requests : 0,
    toolCalls: v.toolCalls,
    toolErrors: v.toolErrors,
  }))
}
