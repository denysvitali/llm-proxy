import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import {
  Box,
  Card,
  Code,
  Divider,
  Drawer,
  Group,
  Loader,
  Paper,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  ThemeIcon,
  Title,
  UnstyledButton,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconArrowsSort, IconInboxOff, IconSearch, IconSearchOff } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchStats } from '../api'
import type { ModelStat } from '../api'
import { fmtInt, fmtPct, fmtSec, fmtTps } from '../format'
import { useChartPalette } from '../palette'
import UptimeBadge from '../components/UptimeBadge'
import PercentileBars from '../components/PercentileBars'
import TokenMixBar, { TokenLegend, type MixSegment } from '../components/TokenMixBar'
import { Fade } from '../App'

type SortKey =
  | 'model'
  | 'requests'
  | 'uptime'
  | 'ttft'
  | 'e2e'
  | 'tps'
  | 'cache'
  | 'tools'
  | 'toolErr'

const columns: { key: SortKey; label: string; numeric?: boolean }[] = [
  { key: 'model', label: 'Backend / model' },
  { key: 'requests', label: 'Requests', numeric: true },
  { key: 'uptime', label: 'Uptime' },
  { key: 'ttft', label: 'TTFT p50/p90/p99', numeric: true },
  { key: 'e2e', label: 'E2E p50/p90/p99', numeric: true },
  { key: 'tps', label: 'tok/s p50', numeric: true },
  { key: 'cache', label: 'Cache hit', numeric: true },
  { key: 'tools', label: 'Tool calls', numeric: true },
  { key: 'toolErr', label: 'Tool err', numeric: true },
]

const sortOptions = [
  { value: 'requests', label: 'Requests' },
  { value: 'uptime', label: 'Uptime' },
  { value: 'ttft', label: 'TTFT (p50)' },
  { value: 'e2e', label: 'E2E latency (p50)' },
  { value: 'tps', label: 'Throughput (p50)' },
  { value: 'cache', label: 'Cache hit' },
  { value: 'tools', label: 'Tool calls' },
  { value: 'toolErr', label: 'Tool error rate' },
  { value: 'model', label: 'Name' },
]

// Token-kind segments in fixed categorical slot order; every chart on this
// page (cards, drawer) draws the same kind in the same color.
function mixSegments(m: ModelStat, colors: string[]): MixSegment[] {
  return [
    { name: 'input', color: colors[0], value: m.input_tokens },
    { name: 'output', color: colors[1], value: m.output_tokens },
    { name: 'cache read', color: colors[2], value: m.cache_read_tokens },
    { name: 'cache write', color: colors[3], value: m.cache_write_tokens },
  ]
}

function sortValue(m: ModelStat, key: SortKey): string | number {
  switch (key) {
    case 'model':
      return `${m.backend}/${m.model}`
    case 'requests':
      return m.requests
    case 'uptime':
      return m.uptime
    case 'ttft':
      return m.ttft_seconds.p50
    case 'e2e':
      return m.e2e_seconds.p50
    case 'tps':
      return m.throughput_tps.p50
    case 'cache':
      return m.cache_rate
    case 'tools':
      return m.tool_calls
    case 'toolErr':
      return m.tool_error_rate
  }
}

export default function ModelsPage() {
  const q = useQuery({ queryKey: ['stats'], queryFn: fetchStats })
  // Memoized so the rows useMemo below sees a stable identity between fetches.
  const models = useMemo(() => q.data?.models ?? [], [q.data])
  const pal = useChartPalette()
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  const [filter, setFilter] = useState('')
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: 'requests', dir: -1 })
  const [selected, setSelected] = useState<ModelStat | null>(null)

  const rows = useMemo(() => {
    const f = filter.trim().toLowerCase()
    return models
      .filter((m) => !f || `${m.backend}/${m.model}`.toLowerCase().includes(f))
      .sort((a, b) => {
        const va = sortValue(a, sort.key)
        const vb = sortValue(b, sort.key)
        if (typeof va === 'string' || typeof vb === 'string')
          return String(va).localeCompare(String(vb)) * sort.dir
        return (va - vb) * sort.dir
      })
  }, [models, filter, sort])

  function toggleSort(key: SortKey) {
    setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: key === 'model' ? 1 : -1 }))
  }

  return (
    <Fade fetching={q.isFetching}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm">
          <div>
            <Title order={4} mb={2}>Models</Title>
            <Text size="xs" c="dimmed">
              {models.length} tracked · tap a {isMobile ? 'card' : 'row'} for full percentiles
            </Text>
          </div>
          <TextInput
            leftSection={<IconSearch size={14} />}
            placeholder="Filter backend or model…"
            value={filter}
            onChange={(e) => setFilter(e.currentTarget.value)}
            style={{ flex: isMobile ? '1 1 100%' : '0 0 280px' }}
          />
        </Group>

        {isMobile && rows.length > 0 && (
          <Group gap="xs" wrap="nowrap">
            <Select
              leftSection={<IconArrowsSort size={14} />}
              data={sortOptions}
              value={sort.key}
              onChange={(v) => v && setSort((s) => ({ key: v as SortKey, dir: s.dir }))}
              allowDeselect={false}
              flex={1}
              size="sm"
            />
            <UnstyledButton
              onClick={() => setSort((s) => ({ ...s, dir: s.dir === 1 ? -1 : 1 }))}
              px="sm"
              py={7}
              style={{
                borderRadius: 'var(--mantine-radius-md)',
                border: '1px solid var(--mantine-color-default-border)',
                fontSize: 'var(--mantine-font-size-sm)',
                fontWeight: 600,
                whiteSpace: 'nowrap',
              }}
            >
              {sort.dir === 1 ? 'Asc ↑' : 'Desc ↓'}
            </UnstyledButton>
          </Group>
        )}

        {q.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={
              models.length === 0 ? (
                <IconInboxOff size={20} stroke={1.6} />
              ) : (
                <IconSearchOff size={20} stroke={1.6} />
              )
            }
            title={models.length === 0 ? 'No model traffic yet' : 'No models match that filter'}
            hint={
              models.length === 0
                ? 'Send a request through the proxy and per-model stats will land here.'
                : 'Try a shorter fragment of the backend or model name.'
            }
          />
        ) : isMobile ? (
          <SimpleGrid cols={1} spacing="sm">
            {rows.map((m) => (
              <ModelCard
                key={`${m.backend}/${m.model}`}
                stat={m}
                colors={pal.series}
                onClick={() => setSelected(m)}
              />
            ))}
          </SimpleGrid>
        ) : (
          <ScrollArea>
            {/* Striped + highlight-on-hover keeps wide rows scannable; the
                  cursor signals the row opens the detail drawer. */}
            <Table verticalSpacing="sm" horizontalSpacing="md" highlightOnHover striped>
              <Table.Thead>
                <Table.Tr>
                  {columns.map((c) => (
                    <Table.Th key={c.key} ta={c.numeric ? 'right' : undefined}>
                      <UnstyledButton onClick={() => toggleSort(c.key)}>
                        <Group gap={4} wrap="nowrap" justify={c.numeric ? 'flex-end' : 'flex-start'}>
                          <Text size="xs" fw={600} c="dimmed">
                            {c.label}
                            {sort.key === c.key && (sort.dir === 1 ? ' ↑' : ' ↓')}
                          </Text>
                        </Group>
                      </UnstyledButton>
                    </Table.Th>
                  ))}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((m) => (
                  <Table.Tr
                    key={`${m.backend}/${m.model}`}
                    onClick={() => setSelected(m)}
                    style={{ cursor: 'pointer' }}
                  >
                    <Table.Td>
                      {/* Backend as a muted eyebrow above the model name —
                            the model is what you scan for. */}
                      <Box style={{ minWidth: 0 }}>
                        <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>
                          {m.backend}
                        </Text>
                        <Code>{m.model}</Code>
                      </Box>
                    </Table.Td>
                    <Num td={fmtInt(m.requests)} />
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        <UptimeBadge uptime={m.uptime} requests={m.requests} />
                      </Group>
                    </Table.Td>
                    <Num td={fmtSec(m.ttft_seconds.p50)} title={`p90 ${fmtSec(m.ttft_seconds.p90)} · p99 ${fmtSec(m.ttft_seconds.p99)}`} />
                    <Num td={fmtSec(m.e2e_seconds.p50)} title={`p90 ${fmtSec(m.e2e_seconds.p90)} · p99 ${fmtSec(m.e2e_seconds.p99)}`} />
                    <Num td={fmtTps(m.throughput_tps.p50)} title={`p90 ${fmtTps(m.throughput_tps.p90)} · p99 ${fmtTps(m.throughput_tps.p99)}`} />
                    <Num td={fmtPct(m.cache_rate)} />
                    <Num td={fmtInt(m.tool_calls)} />
                    <Num td={fmtPct(m.tool_error_rate)} />
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        )}
      </Stack>

      <Drawer
        opened={!!selected}
        onClose={() => setSelected(null)}
        position="right"
        size={isMobile ? '100%' : 'lg'}
        title={
          selected && (
            <Box style={{ minWidth: 0 }}>
              <Text size="xs" c="dimmed" tt="uppercase" fw={600} lh={1.2}>
                {selected.backend}
              </Text>
              <Group gap="xs" wrap="nowrap">
                <Text fw={700} truncate>
                  {selected.model}
                </Text>
                <UptimeBadge uptime={selected.uptime} requests={selected.requests} />
              </Group>
            </Box>
          )
        }
      >
        {selected && <ModelDetail stat={selected} colors={pal.series} />}
      </Drawer>
    </Fade>
  )
}

function ModelCard({
  stat: m,
  colors,
  onClick,
}: {
  stat: ModelStat
  colors: string[]
  onClick: () => void
}) {
  const tokTotal =
    m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_tokens
  return (
    <Card withBorder radius="lg" p="md" onClick={onClick} style={{ cursor: 'pointer' }}>
      <Group justify="space-between" wrap="nowrap" gap="xs" mb={8}>
        <Box style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
            {m.backend}
          </Text>
          <Text fw={600} truncate>
            {m.model}
          </Text>
        </Box>
        <UptimeBadge uptime={m.uptime} requests={m.requests} />
      </Group>
      {/* Short labels: the drawer owns the verbose names; the card is a glance
            surface. Latency pair kept adjacent (TTFT then E2E). */}
      <SimpleGrid cols={3} spacing="xs">
        <Metric label="Requests" value={fmtInt(m.requests)} />
        <Metric label="TTFT" value={fmtSec(m.ttft_seconds.p50)} />
        <Metric label="E2E" value={fmtSec(m.e2e_seconds.p50)} />
        <Metric label="tok/s" value={fmtTps(m.throughput_tps.p50)} />
        <Metric label="Cache" value={fmtPct(m.cache_rate)} />
        <Metric label="Tool err" value={fmtPct(m.tool_error_rate)} />
      </SimpleGrid>
      {/* Slim token-mix strip: cache share is visible at a glance without
            reading any number. Hidden until tokens exist to avoid noise. */}
      {tokTotal > 0 && (
        <Box mt={10}>
          <TokenMixBar segments={mixSegments(m, colors)} height={8} />
        </Box>
      )}
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {value}
      </Text>
    </Box>
  )
}

function Num({ td, title }: { td: string | number; title?: string }) {
  return (
    <Table.Td ta="right" style={{ fontVariantNumeric: 'tabular-nums' }} title={title}>
      {td}
    </Table.Td>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
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
      {children}
    </Box>
  )
}

function StatRow({ label, value }: { label: string; value: string }) {
  return (
    <Group justify="space-between" py={3} wrap="nowrap">
      <Text size="sm" c="dimmed">
        {label}
      </Text>
      <Text size="sm" fw={600} style={{ fontVariantNumeric: 'tabular-nums' }}>
        {value}
      </Text>
    </Group>
  )
}

function EmptyState({
  icon,
  title,
  hint,
}: {
  icon: ReactNode
  title: string
  hint?: string
}) {
  return (
    <Stack align="center" py="xl" gap={6}>
      <ThemeIcon variant="light" color="gray" size="lg" radius="xl">
        {icon}
      </ThemeIcon>
      <Text fw={600}>{title}</Text>
      {hint && (
        <Text size="sm" c="dimmed" ta="center" maw={340}>
          {hint}
        </Text>
      )}
    </Stack>
  )
}

function ModelDetail({ stat, colors }: { stat: ModelStat; colors: string[] }) {
  const segs = mixSegments(stat, colors)
  const totalTok = segs.reduce((s, x) => s + x.value, 0)
  // TTFT and E2E share one time scale so their bar lengths are directly
  // comparable; throughput keeps its own scale (different unit).
  const latMax = Math.max(stat.ttft_seconds.p99, stat.e2e_seconds.p99)

  return (
    <Stack gap="lg">
      <Section title="Latency">
        <Text size="xs" fw={600} c="dimmed" mb={4}>
          Time to first token
        </Text>
        <PercentileBars values={stat.ttft_seconds} unit="s" max={latMax} />
        <Text size="xs" fw={600} c="dimmed" mt="xs" mb={4}>
          End-to-end
        </Text>
        <PercentileBars values={stat.e2e_seconds} unit="s" max={latMax} />
        <Text size="xs" c="dimmed" mt={6}>
          Both share one time scale — bar lengths compare directly.
        </Text>
      </Section>
      <Divider />
      <Section title="Throughput (tokens/sec)">
        <PercentileBars values={stat.throughput_tps} unit="tok/s" />
      </Section>
      <Divider />
      <Section
        title={`Tokens · ${fmtInt(totalTok)} total · cache hit ${fmtPct(stat.cache_rate)}`}
      >
        <TokenMixBar segments={segs} height={20} />
        <TokenLegend segments={segs} showPercent />
      </Section>
      <Divider />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <Paper withBorder radius="md" p="sm">
          <Section title="Requests">
            <StatRow label="Total" value={fmtInt(stat.requests)} />
            <StatRow label="Succeeded" value={fmtInt(stat.successes)} />
            <StatRow label="Failed" value={fmtInt(stat.requests - stat.successes)} />
          </Section>
        </Paper>
        <Paper withBorder radius="md" p="sm">
          <Section title="Tool calls">
            <StatRow label="Calls" value={fmtInt(stat.tool_calls)} />
            <StatRow label="Errors" value={fmtInt(stat.tool_errors)} />
            <StatRow label="Error rate" value={fmtPct(stat.tool_error_rate)} />
          </Section>
        </Paper>
      </SimpleGrid>
    </Stack>
  )
}
