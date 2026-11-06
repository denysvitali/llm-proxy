import {
  ActionIcon,
  Box,
  Card,
  Group,
  SimpleGrid,
  Table,
  Text,
  UnstyledButton,
} from '@mantine/core'
import { IconChevronRight } from '@tabler/icons-react'
import type { ModelStat } from '../../api'
import { fmtInt, fmtSec, fmtTps } from '../../format'
import UptimeBadge from '../UptimeBadge'
import { SuccessMeter } from './SuccessMeter'
import type { ModelSort, SortKey } from './modelData'

const columns: { key: SortKey; label: string }[] = [
  { key: 'model', label: 'Model / provider' },
  { key: 'requests', label: 'Requests' },
  { key: 'success', label: 'Success' },
  { key: 'ttft', label: 'First token' },
  { key: 'e2e', label: 'Response' },
  { key: 'tps', label: 'Tokens / sec' },
]

// Dense data grid for recorded traffic. Numbers are right-aligned and tabular;
// success is a thin inline meter rather than a lone percentage, so a row's
// health is readable before the exact figure is. Unknown stays an em dash.
export default function ModelTraffic({
  models,
  mobile,
  sort,
  onSort,
  onInspect,
}: {
  models: ModelStat[]
  mobile: boolean
  sort: ModelSort
  onSort: (key: SortKey) => void
  onInspect: (model: ModelStat) => void
}) {
  if (mobile)
    return (
      <div className="catalog-mobile-traffic">
        {models.map((model) => (
          <ModelCard
            key={`${model.backend}/${model.model}`}
            model={model}
            onInspect={() => onInspect(model)}
          />
        ))}
      </div>
    )
  return (
    <div className="models-table-scroll">
      <Table
        miw={750}
        className="models-table"
        aria-label="Model traffic and latency"
      >
        <Table.Thead>
          <Table.Tr>
            {columns.map((column) => (
              <Table.Th
                key={column.key}
                ta={column.key === 'model' ? 'left' : 'right'}
                aria-sort={sort.key === column.key ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'}
                data-sorted={sort.key === column.key ? 'true' : undefined}
              >
                <UnstyledButton
                  className="models-table-sort"
                  onClick={() => onSort(column.key)}
                  aria-label={`Sort by ${column.label}`}
                  data-active={sort.key === column.key ? 'true' : undefined}
                >
                  <Text component="span" size="xs" className="models-table-col-label">
                    {column.label}
                  </Text>
                  <span className="models-table-sort-arrow" aria-hidden>
                    {sort.key === column.key ? (sort.dir === 1 ? '↑' : '↓') : ''}
                  </span>
                </UnstyledButton>
              </Table.Th>
            ))}
            <Table.Th className="models-table-actions-head">
              <span className="visually-hidden">Details</span>
            </Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {models.map((model) => {
            const errored = model.requests > 0 && model.successes / model.requests < 0.9
            return (
              <Table.Tr
                key={`${model.backend}/${model.model}`}
                className="models-table-row"
                onClick={() => onInspect(model)}
              >
                <Table.Td>
                  <Text className="catalog-model-id models-table-model" title={model.model}>
                    {model.model}
                  </Text>
                  <Group gap={8} mt={4} wrap="nowrap">
                    <Text size="xs" c="dimmed" className="models-table-backend">
                      {model.backend}
                    </Text>
                    <UptimeBadge uptime={model.uptime} requests={model.requests} />
                  </Group>
                </Table.Td>
                <Table.Td ta="right" className="tabular models-table-num">
                  {fmtInt(model.requests)}
                </Table.Td>
                <Table.Td ta="right" className={errored ? 'warning-value' : undefined}>
                  <SuccessMeter rate={model.successes / model.requests} requests={model.requests} />
                </Table.Td>
                <Table.Td ta="right" className="tabular models-table-num">
                  {fmtSec(model.ttft_seconds.p50)}
                </Table.Td>
                <Table.Td ta="right" className="tabular models-table-num">
                  {fmtSec(model.e2e_seconds.p50)}
                </Table.Td>
                <Table.Td ta="right" className="tabular models-table-num">
                  {fmtTps(model.throughput_tps.p50)}
                </Table.Td>
                <Table.Td className="models-table-actions">
                  <ActionIcon
                    size={36}
                    variant="subtle"
                    color="gray"
                    aria-label={`Open details for ${model.backend} ${model.model}`}
                    onClick={(event) => {
                      event.stopPropagation()
                      onInspect(model)
                    }}
                  >
                    <IconChevronRight size={17} />
                  </ActionIcon>
                </Table.Td>
              </Table.Tr>
            )
          })}
        </Table.Tbody>
      </Table>
    </div>
  )
}

function ModelCard({ model, onInspect }: { model: ModelStat; onInspect: () => void }) {
  const rate = model.successes / model.requests
  return (
    <Card p="md" radius="lg" data-model-card miw={0} className="catalog-mobile-card">
      <Group justify="space-between" wrap="nowrap" align="flex-start" mb="sm" gap="xs">
        <Box miw={0}>
          <Text size="xs" c="dimmed" mb={2}>
            {model.backend}
          </Text>
          <Text className="catalog-model-id" fw={600}>
            {model.model}
          </Text>
        </Box>
        <ActionIcon
          size={44}
          variant="light"
          color="gray"
          aria-label={`Open details for ${model.backend} ${model.model}`}
          onClick={onInspect}
        >
          <IconChevronRight size={18} />
        </ActionIcon>
      </Group>
      <Group justify="space-between" mb="sm" gap="xs" wrap="nowrap">
        <UptimeBadge uptime={model.uptime} requests={model.requests} />
        <span className="catalog-mobile-success">
          <SuccessMeter rate={rate} requests={model.requests} compact />
        </span>
      </Group>
      <SimpleGrid cols={3} spacing="sm" className="catalog-mobile-metrics-row">
        <Metric label="Requests" value={fmtInt(model.requests)} />
        <Metric label="First token" value={fmtSec(model.ttft_seconds.p50)} />
        <Metric label="Tokens / sec" value={fmtTps(model.throughput_tps.p50)} />
      </SimpleGrid>
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box miw={0}>
      <Text size="xs" c="dimmed" className="micro-label">
        {label}
      </Text>
      <Text size="sm" fw={650} mt={3} className="tabular">
        {value}
      </Text>
    </Box>
  )
}
