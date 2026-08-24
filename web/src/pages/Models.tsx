import { useMemo, useState } from 'react'
import {
  Box,
  Card,
  Code,
  Divider,
  Drawer,
  Group,
  Loader,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconArrowsSort, IconSearch } from '@tabler/icons-react'
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
  const models = q.data?.models ?? []
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
          <Text c="dimmed" py="xl" ta="center">
            {models.length === 0 ? 'No model traffic recorded yet.' : 'No models match that filter.'}
          </Text>
        ) : isMobile ? (
          <SimpleGrid cols={1} spacing="sm">
            {rows.map((m) => (
              <ModelCard key={`${m.backend}/${m.model}`} stat={m} onClick={() => setSelected(m)} />
            ))}
          </SimpleGrid>
        ) : (
          <ScrollArea>
            <Table verticalSpacing="sm" horizontalSpacing="md" highlightOnHover>
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
                      <Code>{m.backend}</Code> <Code>{m.model}</Code>
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
            <Group gap="xs">
              <Code>{selected.backend}</Code>
              <Code>{selected.model}</Code>
              <UptimeBadge uptime={selected.uptime} requests={selected.requests} />
            </Group>
          )
        }
      >
        {selected && <ModelDetail stat={selected} colors={pal.series} />}
      </Drawer>
    </Fade>
  )
}

function ModelCard({ stat: m, onClick }: { stat: ModelStat; onClick: () => void }) {
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
      <SimpleGrid cols={3} spacing="xs">
        <Metric label="Requests" value={fmtInt(m.requests)} />
        <Metric label="TTFT p50" value={fmtSec(m.ttft_seconds.p50)} />
        <Metric label="tok/s p50" value={fmtTps(m.throughput_tps.p50)} />
        <Metric label="E2E p50" value={fmtSec(m.e2e_seconds.p50)} />
        <Metric label="Cache hit" value={fmtPct(m.cache_rate)} />
        <Metric label="Tool err" value={fmtPct(m.tool_error_rate)} />
      </SimpleGrid>
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

function ModelDetail({ stat, colors }: { stat: ModelStat; colors: string[] }) {
  const segs: MixSegment[] = [
    { name: 'input', color: colors[0], value: stat.input_tokens },
    { name: 'output', color: colors[1], value: stat.output_tokens },
    { name: 'cache read', color: colors[2], value: stat.cache_read_tokens },
    { name: 'cache write', color: colors[3], value: stat.cache_write_tokens },
  ]
  const totalTok = segs.reduce((s, x) => s + x.value, 0)
  return (
    <Stack gap="lg">
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          Time to first token
        </Text>
        <PercentileBars values={stat.ttft_seconds} unit="s" />
      </Box>
      <Divider />
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          End-to-end latency
        </Text>
        <PercentileBars values={stat.e2e_seconds} unit="s" />
      </Box>
      <Divider />
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          Throughput (tokens/sec)
        </Text>
        <PercentileBars values={stat.throughput_tps} unit="tok/s" />
      </Box>
      <Divider />
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          Tokens ({fmtInt(totalTok)} total · cache hit {fmtPct(stat.cache_rate)})
        </Text>
        <TokenMixBar segments={segs} height={20} />
        <TokenLegend segments={segs} />
      </Box>
      <Divider />
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          Tool calls
        </Text>
        <Group gap="xl">
          <Text size="sm">
            calls <Text span fw={700}>{fmtInt(stat.tool_calls)}</Text>
          </Text>
          <Text size="sm">
            errors <Text span fw={700}>{fmtInt(stat.tool_errors)}</Text>
          </Text>
          <Text size="sm">
            error rate <Text span fw={700}>{fmtPct(stat.tool_error_rate)}</Text>
          </Text>
        </Group>
      </Box>
      <Divider />
      <Box>
        <Text size="xs" tt="uppercase" c="dimmed" mb={6}>
          Requests
        </Text>
        <Group gap="xl">
          <Text size="sm">
            total <Text span fw={700}>{fmtInt(stat.requests)}</Text>
          </Text>
          <Text size="sm">
            succeeded <Text span fw={700}>{fmtInt(stat.successes)}</Text>
          </Text>
          <Text size="sm">
            failed <Text span fw={700}>{fmtInt(stat.requests - stat.successes)}</Text>
          </Text>
        </Group>
      </Box>
    </Stack>
  )
}
