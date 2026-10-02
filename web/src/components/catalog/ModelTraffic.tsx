import {
  ActionIcon,
  Box,
  Button,
  Card,
  Group,
  ScrollArea,
  SimpleGrid,
  Table,
  Text,
  UnstyledButton,
} from '@mantine/core'
import { IconChevronRight } from '@tabler/icons-react'
import type { ModelStat } from '../../api'
import { fmtInt, fmtSec, fmtTps } from '../../format'
import UptimeBadge from '../UptimeBadge'
import { observedRate, type ModelSort, type SortKey } from './modelData'

const columns: { key: SortKey; label: string }[] = [
  { key: 'model', label: 'Model / provider' },
  { key: 'requests', label: 'Requests' },
  { key: 'success', label: 'Success' },
  { key: 'ttft', label: 'First token' },
  { key: 'e2e', label: 'Response' },
  { key: 'tps', label: 'Tokens / sec' },
]

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
      <SimpleGrid cols={1} spacing="sm">
        {models.map((model) => (
          <ModelCard
            key={`${model.backend}/${model.model}`}
            model={model}
            onInspect={() => onInspect(model)}
          />
        ))}
      </SimpleGrid>
    )
  return (
    <ScrollArea>
      <Table
        miw={750}
        verticalSpacing="md"
        horizontalSpacing="md"
        highlightOnHover
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
              >
                <UnstyledButton
                  mih={40}
                  onClick={() => onSort(column.key)}
                  aria-label={`Sort by ${column.label}`}
                >
                  <Text component="span" size="xs" fw={600}>
                    {column.label}
                    {sort.key === column.key ? (sort.dir === 1 ? ' ↑' : ' ↓') : ''}
                  </Text>
                </UnstyledButton>
              </Table.Th>
            ))}
            <Table.Th>
              <span className="visually-hidden">Details</span>
            </Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {models.map((model) => (
            <Table.Tr
              key={`${model.backend}/${model.model}`}
              onClick={() => onInspect(model)}
              className="catalog-traffic-row"
            >
              <Table.Td className="catalog-identity-cell">
                <Text size="sm" fw={600} className="catalog-model-name">
                  {model.model}
                </Text>
                <Group gap={8} mt={5}>
                  <Text size="xs" c="dimmed">
                    {model.backend}
                  </Text>
                  <UptimeBadge uptime={model.uptime} requests={model.requests} />
                </Group>
              </Table.Td>
              <Table.Td ta="right" fw={600}>
                {fmtInt(model.requests)}
              </Table.Td>
              <Table.Td
                ta="right"
                className={
                  model.requests > 0 && model.successes / model.requests < 0.9
                    ? 'catalog-warning-value'
                    : undefined
                }
              >
                {observedRate(model.successes / model.requests, model.requests)}
              </Table.Td>
              <Table.Td ta="right">{fmtSec(model.ttft_seconds.p50)}</Table.Td>
              <Table.Td ta="right">{fmtSec(model.e2e_seconds.p50)}</Table.Td>
              <Table.Td ta="right">{fmtTps(model.throughput_tps.p50)}</Table.Td>
              <Table.Td>
                <ActionIcon
                  size={40}
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
          ))}
        </Table.Tbody>
      </Table>
    </ScrollArea>
  )
}

function ModelCard({ model, onInspect }: { model: ModelStat; onInspect: () => void }) {
  return (
    <Card withBorder radius="md" p="md" className="catalog-traffic-card" data-model-card>
      <Group justify="space-between" wrap="nowrap" align="flex-start" mb="sm">
        <Box miw={0}>
          <Text size="xs" c="dimmed" mb={4}>
            {model.backend}
          </Text>
          <Text fw={650} className="catalog-model-name">
            {model.model}
          </Text>
        </Box>
        <ActionIcon
          size={44}
          variant="light"
          color="brand"
          aria-label={`Open details for ${model.backend} ${model.model}`}
          onClick={onInspect}
        >
          <IconChevronRight size={18} />
        </ActionIcon>
      </Group>
      <Group justify="space-between" mb="md">
        <UptimeBadge uptime={model.uptime} requests={model.requests} />
        <Text size="xs" c="dimmed">
          {observedRate(model.successes / model.requests, model.requests)} success
        </Text>
      </Group>
      <SimpleGrid cols={3} spacing="sm" className="catalog-mobile-metrics">
        <Metric label="Requests" value={fmtInt(model.requests)} />
        <Metric label="First token" value={fmtSec(model.ttft_seconds.p50)} />
        <Metric label="Tokens / sec" value={fmtTps(model.throughput_tps.p50)} />
      </SimpleGrid>
      <Button fullWidth variant="subtle" size="xs" mt="sm" mih={40} onClick={onInspect}>
        Explore performance
      </Button>
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text size="sm" fw={650} mt={4} className="tabular">
        {value}
      </Text>
    </Box>
  )
}
