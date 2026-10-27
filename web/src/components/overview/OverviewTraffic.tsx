import { Accordion, Badge, Box, Button, Card, Group, SimpleGrid, Stack, Text, Tooltip } from '@mantine/core'
import { IconArrowUpRight, IconChartBar, IconShieldCheck } from '@tabler/icons-react'
import { Link } from 'react-router-dom'
import type { ModelStat } from '../../api'
import { fmtInt } from '../../format'
import { providerAggregates, providerSegments } from '../../lib/stats'
import { useChartPalette } from '../../palette'
import { EmptyState } from '../EmptyState'
import { PageSection } from '../PageSection'
import StatusChips from '../StatusChips'
import TokenMixBar, { TokenLegend } from '../TokenMixBar'
import UptimeBadge from '../UptimeBadge'

export default function OverviewTraffic({ models }: { models: ModelStat[] }) {
  const palette = useChartPalette()
  const ranked = [...models].filter((model) => model.requests > 0).sort((a, b) => b.requests - a.requests).slice(0, 6)
  const totalRequests = models.reduce((total, model) => total + model.requests, 0)
  const providers = providerAggregates(models).sort((a, b) => b.requests - a.requests)
  const mix = new Map(providerSegments(models, palette.series))

  return (
    <PageSection title="Traffic breakdown" description="All recorded traffic, grouped by model and provider">
      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md" miw={0}>
        <Card p="lg" miw={0}>
          <Group justify="space-between" align="flex-start" gap="xs" mb="md">
            <Box>
              <Text fw={650} fz={14}>Requests by model</Text>
              <Text size="xs" c="dimmed">Ranked by recorded request volume</Text>
            </Box>
            <Button component={Link} to="/models" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All models</Button>
          </Group>
          {!ranked.length ? <EmptyState icon={<IconChartBar size={20} />} title="No model traffic yet" hint="Models are ranked here after the first request." /> : (
            <Stack gap={12} component="ol" m={0} p={0} style={{ listStyle: 'none' }}>
              {ranked.map((model) => {
                const share = totalRequests ? model.requests / totalRequests * 100 : 0
                return (
                  <Stack key={`${model.backend}/${model.model}`} component="li" gap={6} miw={0} m={0} p={0}>
                    <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
                      <Box miw={0}>
                        <Text size="sm" fw={600} style={{ overflowWrap: 'anywhere' }}>{model.model}</Text>
                        <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{model.backend}</Text>
                      </Box>
                      <Box ta="right" style={{ flexShrink: 0 }}>
                        <Text size="sm" fw={600} className="tabular" title={`${model.requests.toLocaleString('en-US')} requests`}>{fmtInt(model.requests)}</Text>
                        <Text size="xs" c="dimmed">{share.toFixed(1)}%</Text>
                      </Box>
                    </Group>
                    <Tooltip label={`${model.backend}/${model.model}: ${model.requests.toLocaleString('en-US')} requests · ${share.toFixed(1)}% of all traffic`}>
                      <div className="overview-rank-track" aria-hidden="true">
                        <div style={{ width: `${share}%`, background: palette.magnitude }} />
                      </div>
                    </Tooltip>
                  </Stack>
                )
              })}
            </Stack>
          )}
          {ranked.length > 0 && <Text size="xs" c="dimmed" mt="md">{ranked.length === 1 ? 'Top model' : `Top ${ranked.length} models`} · share of all requests</Text>}
        </Card>
        <Card p="lg" miw={0}>
          <Group justify="space-between" align="flex-start" gap="xs" mb="md">
            <Box>
              <Text fw={650} fz={14}>Provider health</Text>
              <Text size="xs" c="dimmed">Request success over recorded traffic</Text>
            </Box>
            <Button component={Link} to="/providers" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All providers</Button>
          </Group>
          {!providers.length ? <EmptyState icon={<IconShieldCheck size={20} />} title="No providers with traffic yet" hint="Backend health appears here after the first request is routed." /> : (
            <Accordion variant="default" miw={0}>
              {providers.map((provider) => {
                const segments = mix.get(provider.backend) ?? []
                return (
                  <Accordion.Item key={provider.backend} value={provider.backend}>
                    <Accordion.Control>
                      <Group justify="space-between" gap="xs" wrap="nowrap" miw={0}>
                        <Box miw={0}>
                          <Text size="sm" fw={600} style={{ overflowWrap: 'anywhere' }}>{provider.backend}</Text>
                          <Text size="xs" c="dimmed">{fmtInt(provider.requests)} requests</Text>
                        </Box>
                        <Group gap="sm" wrap="nowrap" style={{ flexShrink: 0 }}>
                          <UptimeBadge uptime={provider.uptime} requests={provider.requests} />
                          <Text size="sm" fw={600} className="tabular">{provider.requests ? `${(provider.uptime * 100).toFixed(1)}%` : '—'}</Text>
                        </Group>
                      </Group>
                    </Accordion.Control>
                    <Accordion.Panel>
                      <Stack gap="sm">
                        <Box>
                          <Text size="xs" fw={600} mb={6}>Token mix</Text>
                          <TokenMixBar segments={segments} height={10} />
                          <TokenLegend segments={segments} compact />
                        </Box>
                        <Group justify="space-between" gap="xs">
                          <Text size="xs" c="dimmed">Upstream status</Text>
                          {Object.keys(provider.statusCodes).length ? <StatusChips codes={provider.statusCodes} /> : <Badge size="xs" variant="light" color="gray">No recorded errors</Badge>}
                        </Group>
                        <Text size="xs" c="dimmed">{fmtInt(provider.toolErrors)} tool errors across {fmtInt(provider.toolCalls)} calls</Text>
                      </Stack>
                    </Accordion.Panel>
                  </Accordion.Item>
                )
              })}
            </Accordion>
          )}
        </Card>
      </SimpleGrid>
    </PageSection>
  )
}
