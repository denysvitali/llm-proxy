import { Accordion, Badge, Box, Button, Card, Group, ScrollArea, SimpleGrid, Stack, Table, Text } from '@mantine/core'
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
  const ranked = [...models].filter((model) => model.requests > 0).sort((a, b) => `${a.backend}/${a.model}`.localeCompare(`${b.backend}/${b.model}`))
  const totalRequests = models.reduce((total, model) => total + model.requests, 0)
  const providers = providerAggregates(models).sort((a, b) => a.backend.localeCompare(b.backend))
  const mix = new Map(providerSegments(models, palette.series))

  return (
    <PageSection title="Traffic breakdown" description="All recorded traffic, grouped by model and provider">
      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md" miw={0}>
        <Card p="lg" miw={0}>
          <Group justify="space-between" align="flex-start" gap="xs" mb="md">
            <Box>
              <Text fw={650} fz={14}>Requests by model</Text>
              <Text size="xs" c="dimmed">Recorded request volume by model</Text>
            </Box>
            <Button component={Link} to="/models" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All models</Button>
          </Group>
          <ScrollArea className="traffic-feed" type="auto" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Requests by model' }}>
          {!ranked.length ? <EmptyState icon={<IconChartBar size={20} />} title="No model traffic yet" hint="Models appear here after the first request." /> : (
            <Table className="traffic-model-table" verticalSpacing="sm">
              <Table.Thead><Table.Tr><Table.Th scope="col">Model</Table.Th><Table.Th scope="col" ta="right">Requests</Table.Th><Table.Th scope="col" ta="right">Share</Table.Th></Table.Tr></Table.Thead>
              <Table.Tbody>
                {ranked.map((model) => {
                  const share = totalRequests ? model.requests / totalRequests * 100 : 0
                  return <Table.Tr key={`${model.backend}/${model.model}`}>
                    <Table.Td><Text size="sm" fw={600} truncate title={model.model}>{model.model}</Text><Text size="xs" c="dimmed" truncate title={model.backend}>{model.backend}</Text></Table.Td>
                    <Table.Td ta="right" className="tabular">{fmtInt(model.requests)}</Table.Td>
                    <Table.Td ta="right" className="tabular">{share.toFixed(1)}%</Table.Td>
                  </Table.Tr>
                })}
              </Table.Tbody>
            </Table>
          )}
          </ScrollArea>
          <Text size="xs" c="dimmed" mt="md">{ranked.length ? `${ranked.length} ${ranked.length === 1 ? 'model' : 'models'} · share of all requests` : 'Share of all requests'}</Text>
        </Card>
        <Card p="lg" miw={0}>
          <Group justify="space-between" align="flex-start" gap="xs" mb="md">
            <Box>
              <Text fw={650} fz={14}>Provider health</Text>
              <Text size="xs" c="dimmed">Request success over recorded traffic</Text>
            </Box>
            <Button component={Link} to="/providers" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All providers</Button>
          </Group>
          <ScrollArea className="traffic-feed" type="auto" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Provider health' }}>
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
          </ScrollArea>
        </Card>
      </SimpleGrid>
    </PageSection>
  )
}
