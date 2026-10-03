import { Accordion, Badge, Box, Button, Card, Group, Stack, Text, Title, Tooltip } from '@mantine/core'
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
      <div className="overview-breakdown-grid">
        <Card withBorder radius="lg" p="lg">
          <div className="overview-card-header">
            <div>
              <Title order={3} size="sm" className="overview-card-title">Requests by model</Title>
              <Text size="xs" c="dimmed" className="overview-card-subtitle">Ranked by recorded request volume</Text>
            </div>
            <Button component={Link} to="/models" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All models</Button>
          </div>
          {!ranked.length ? <EmptyState icon={<IconChartBar size={20} />} title="No model traffic yet" hint="Models are ranked here after the first request." /> : (
            <ol className="overview-model-ranking">
              {ranked.map((model, index) => {
                const share = totalRequests ? model.requests / totalRequests * 100 : 0
                return (
                  <li key={`${model.backend}/${model.model}`}>
                    <Text size="xs" c="dimmed" className="overview-rank-number" aria-hidden="true">
                      {String(index + 1).padStart(2, '0')}
                    </Text>
                    <Box miw={0}>
                      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
                        <Box miw={0}>
                          <Text size="sm" fw={600} className="overview-identifier">{model.model}</Text>
                          <Text size="xs" c="dimmed" className="overview-identifier">{model.backend}</Text>
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
                    </Box>
                  </li>
                )
              })}
            </ol>
          )}
          {ranked.length > 0 && <Text size="xs" c="dimmed" mt="md">Top {ranked.length} models · share of all requests</Text>}
        </Card>
        <Card withBorder radius="lg" p="lg">
          <div className="overview-card-header">
            <div>
              <Title order={3} size="sm" className="overview-card-title">Provider health</Title>
              <Text size="xs" c="dimmed" className="overview-card-subtitle">Request success over recorded traffic</Text>
            </div>
            <Button component={Link} to="/providers" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={14} />}>All providers</Button>
          </div>
          {!providers.length ? <EmptyState icon={<IconShieldCheck size={20} />} title="No providers with traffic yet" hint="Backend health appears here after the first request is routed." /> : (
            <Accordion variant="default" className="overview-provider-list">
              {providers.map((provider) => {
                const segments = mix.get(provider.backend) ?? []
                return (
                  <Accordion.Item key={provider.backend} value={provider.backend}>
                    <Accordion.Control>
                      <div className="overview-provider-row">
                        <Box miw={0}>
                          <Text size="sm" fw={600} className="overview-identifier">{provider.backend}</Text>
                          <Text size="xs" c="dimmed">{fmtInt(provider.requests)} requests</Text>
                        </Box>
                        <UptimeBadge uptime={provider.uptime} requests={provider.requests} />
                        <Text size="sm" fw={600} className="tabular">{provider.requests ? `${(provider.uptime * 100).toFixed(1)}%` : '—'}</Text>
                      </div>
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
      </div>
    </PageSection>
  )
}
