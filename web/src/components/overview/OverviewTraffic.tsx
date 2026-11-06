import { Button, Card, ScrollArea, Table, Text, Tooltip } from '@mantine/core'
import { IconArrowUpRight, IconChartBar, IconShieldCheck } from '@tabler/icons-react'
import { Link } from 'react-router-dom'
import type { ModelStat } from '../../api'
import { fmtInt } from '../../format'
import { providerAggregates, providerSegments, uptimeState } from '../../lib/stats'
import { useChartPalette } from '../../palette'
import { EmptyState } from '../EmptyState'
import { PageSection } from '../PageSection'
import TokenMixBar from '../TokenMixBar'

// How many ranked models fit the instrument panel before the list is capped.
// Six rows + the sticky header fit the feed viewport exactly, so the region
// never cuts a row in half; the rest are one click away on /models.
const MODEL_ROWS = 6

export default function OverviewTraffic({ models }: { models: ModelStat[] }) {
  const palette = useChartPalette()
  const ranked = [...models]
    .filter((model) => model.requests > 0)
    .sort((a, b) => b.requests - a.requests || `${a.backend}/${a.model}`.localeCompare(`${b.backend}/${b.model}`))
  const shown = ranked.slice(0, MODEL_ROWS)
  const rest = ranked.length - shown.length
  const totalRequests = models.reduce((total, model) => total + model.requests, 0)
  const providers = providerAggregates(models).sort((a, b) => b.requests - a.requests || a.backend.localeCompare(b.backend))
  const mix = new Map(providerSegments(models, palette.series))

  return (
    <PageSection title="Traffic breakdown" description="All recorded traffic, grouped by model and provider (independent of the chart range)">
      <div className="overview-traffic-grid">
        <Card p="md" miw={0} className="overview-traffic-card">
          <div className="overview-card-head">
            <div>
              <span className="overview-label">Requests by model</span>
              <Text size="xs" c="dimmed">Ranked by recorded volume</Text>
            </div>
            <Button component={Link} to="/models" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={13} />}>All models</Button>
          </div>
          {!shown.length ? <EmptyState icon={<IconChartBar size={20} />} title="No model traffic yet" hint="Models appear here after the first request." /> : (
            <ScrollArea className="traffic-feed traffic-model-feed" type="auto" scrollbars="y" offsetScrollbars="present" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Requests by model' }}>
              <Table className="traffic-model-table" verticalSpacing={7} horizontalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th scope="col">Model</Table.Th>
                    <Table.Th scope="col" ta="right">Requests</Table.Th>
                    <Table.Th scope="col" ta="right">Share</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {shown.map((model) => {
                    const share = totalRequests ? model.requests / totalRequests * 100 : 0
                    return (
                      <Table.Tr key={`${model.backend}/${model.model}`}>
                        <Table.Td>
                          <Text size="xs" className="overview-ident" fw={600} title={model.model}>{model.model}</Text>
                          <Text size="xs" c="dimmed" className="overview-ident" title={model.backend}>{model.backend}</Text>
                        </Table.Td>
                        <Table.Td ta="right" className="tabular" fz={13}>{fmtInt(model.requests)}</Table.Td>
                        <Table.Td ta="right" className="tabular" fz={13} c="dimmed">{share.toFixed(1)}%</Table.Td>
                      </Table.Tr>
                    )
                  })}
                </Table.Tbody>
              </Table>
            </ScrollArea>
          )}
          <div className="overview-card-foot">
            {rest > 0 ? (
              <Button component={Link} to="/models" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={13} />}>
                +{rest} more {rest === 1 ? 'model' : 'models'}
              </Button>
            ) : (
              <Text size="xs" c="dimmed">Share of all recorded requests</Text>
            )}
          </div>
        </Card>

        <Card p="md" miw={0} className="overview-traffic-card">
          <div className="overview-card-head">
            <div>
              <span className="overview-label">Provider health</span>
              <Text size="xs" c="dimmed">Success rate and token mix per provider</Text>
            </div>
            <Button component={Link} to="/providers" variant="subtle" size="compact-xs" rightSection={<IconArrowUpRight size={13} />}>All providers</Button>
          </div>
          {!providers.length ? <EmptyState icon={<IconShieldCheck size={20} />} title="No providers with traffic yet" hint="Backend health appears here after the first request is routed." /> : (
            <ScrollArea className="traffic-feed" type="auto" scrollbars="y" offsetScrollbars="present" viewportProps={{ tabIndex: 0, role: 'region', 'aria-label': 'Provider health' }}>
              <div className="overview-provider-rows">
                {providers.map((provider) => {
                  const segments = mix.get(provider.backend) ?? []
                  const hasTraffic = provider.requests > 0
                  const percent = hasTraffic ? provider.uptime * 100 : undefined
                  const tone = hasTraffic ? uptimeState(provider.uptime) : undefined
                  return (
                    <div key={provider.backend} className="overview-provider-row">
                      <Text size="xs" fw={600} className="overview-ident overview-provider-name">{provider.backend}</Text>
                      <div
                        className="overview-success"
                        role="img"
                        aria-label={percent === undefined ? `${provider.backend}: no requests recorded` : `${provider.backend}: ${percent.toFixed(1)}% of requests succeeded`}
                      >
                        <Tooltip label={percent === undefined ? 'No requests recorded yet' : `${percent.toFixed(1)}% succeeded`} events={{ hover: true, focus: true, touch: true }}>
                          <div className="overview-success-track" tabIndex={0}>
                            <span className="overview-success-fill" data-tone={tone} style={{ width: `${percent ?? 0}%` }} />
                          </div>
                        </Tooltip>
                      </div>
                      <div className="overview-provider-stats">
                        <Text size="xs" c="dimmed" className="tabular">{fmtInt(provider.requests)} requests</Text>
                        <Text size="xs" fw={650} className="tabular overview-provider-percent" data-tone={tone}>
                          {percent === undefined ? '—' : `${percent.toFixed(1)}%`}
                        </Text>
                      </div>
                      <div className="overview-token-mix" title={`${provider.toolErrors} tool errors across ${provider.toolCalls} calls`}>
                        {segments.length ? <TokenMixBar segments={segments} height={6} /> : <span className="overview-token-none">no tokens</span>}
                      </div>
                    </div>
                  )
                })}
              </div>
            </ScrollArea>
          )}
          <div className="overview-card-foot">
            <Text size="xs" c="dimmed">Success is measured over all retained traffic</Text>
          </div>
        </Card>
      </div>
    </PageSection>
  )
}
