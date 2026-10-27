import { useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Group,
  Loader,
  Select,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useClipboard, useMediaQuery } from '@mantine/hooks'
import { IconCheck, IconChevronLeft, IconChevronRight, IconCopy, IconCube } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { plural } from '../../format'
import { dashboardQueries } from '../../queries'
import SearchInput from '../SearchInput'

export default function ModelCatalog() {
  const query = useQuery(dashboardQueries.overview())
  const [filter, setFilter] = useState('')
  const [provider, setProvider] = useState('all')
  const [page, setPage] = useState(0)
  const mobile = useMediaQuery('(max-width: 48em)') ?? false
  const pageSize = mobile ? 6 : 18
  const backends = query.data?.backends ?? []
  const enabled = backends.filter((backend) => backend?.enabled)
  const catalog = useMemo(() => {
    const entries = new Map<string, string[]>()
    for (const backend of query.data?.backends ?? []) {
      if (!backend?.enabled || !backend.catalogOK) continue
      for (const id of backend.models ?? [])
        entries.set(id, [...new Set([...(entries.get(id) ?? []), backend.name])])
    }
    return [...entries].map(([id, providers]) => ({ id, providers })).sort((a, b) => a.id.localeCompare(b.id))
  }, [query.data])
  const visible = catalog.filter(
    (entry) =>
      (provider === 'all' || entry.providers.includes(provider)) &&
      `${entry.id} ${entry.providers.join(' ')}`.toLowerCase().includes(filter.trim().toLowerCase()),
  )
  const lastPage = Math.max(0, Math.ceil(visible.length / pageSize) - 1)
  const currentPage = Math.min(page, lastPage)
  const failed = enabled.filter((backend) => !backend.catalogOK).length
  return (
    <Card component="section" aria-label="Available model catalog" miw={0}>
      <Group justify="space-between" gap="xs" mb="md" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" miw={0}>
          <span className="stat-tile-icon" aria-hidden>
            <IconCube size={17} />
          </span>
          <Box miw={0}>
            <Title order={2} size="h4" fz={15}>
              Available models
            </Title>
            <Text size="xs" c="dimmed">
              Search the enabled provider catalogs, then copy the complete ID for your client.
            </Text>
          </Box>
        </Group>
        <Badge variant="light" color="indigo" style={{ flexShrink: 0 }}>
          {query.data ? plural(catalog.length, 'model') : '— models'}
        </Badge>
      </Group>
      <Group gap="sm" align="flex-start" mb="sm" wrap="wrap">
        <Box flex={1} miw={220}>
          <SearchInput
            label="Find an available model"
            placeholder="Search model IDs or providers…"
            value={filter}
            onChange={(value) => {
              setFilter(value)
              setPage(0)
            }}
          />
        </Box>
        <Select
          w={220}
          aria-label="Filter available models by provider"
          value={provider}
          allowDeselect={false}
          onChange={(value) => {
            setProvider(value ?? 'all')
            setPage(0)
          }}
          data={[
            { value: 'all', label: 'All providers' },
            ...enabled.map((backend) => ({ value: backend.name, label: backend.name })),
          ]}
        />
      </Group>
      {query.isPending ? (
        <Group py="lg" justify="center" role="status">
          <Loader size="sm" />
          <Text size="sm">Loading model catalog…</Text>
        </Group>
      ) : query.isError ? (
        <Alert mt="md" color="red" title="Model catalog unavailable">
          <Stack gap="xs" align="flex-start">
            <Text size="sm">{query.error.message}</Text>
            <Button variant="light" mih={44} onClick={() => query.refetch()}>
              Retry
            </Button>
          </Stack>
        </Alert>
      ) : (
        <>
          {failed > 0 && (
            <Text mt="sm" size="xs" c="dimmed">
              {failed} provider catalog{failed === 1 ? ' is' : 's are'} unavailable.
            </Text>
          )}
          <Group justify="space-between" gap="xs" mt="md" mb="sm">
            <Text size="sm" fw={600}>Model catalog</Text>
            <Text size="xs" c="dimmed" aria-live="polite">
              {visible.length ? `${visible.length} available` : 'No results'} · {plural(enabled.length, 'enabled provider')}
            </Text>
          </Group>
          <div className="catalog-model-grid">
            {visible.slice(currentPage * pageSize, (currentPage + 1) * pageSize).map((entry) => (
              <CatalogModel key={entry.id} {...entry} />
            ))}
          </div>
          {visible.length === 0 && (
            <Text py="xl" ta="center" size="sm" c="dimmed">
              {catalog.length === 0
                ? 'No provider models are available.'
                : 'No available models match that search.'}
            </Text>
          )}
          <Group className="catalog-pagination" justify="space-between" gap="xs" mt="md">
            <Text size="xs" c="dimmed" aria-live="polite">
              {visible.length
                ? `${currentPage * pageSize + 1}–${Math.min((currentPage + 1) * pageSize, visible.length)} of ${visible.length}`
                : '0'}{' '}
              available models
            </Text>
            <Group gap={4}>
              <ActionIcon
                size={36}
                variant="default"
                aria-label="Previous catalog page"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                <IconChevronLeft size={16} />
              </ActionIcon>
              <Text size="xs" c="dimmed" px={6}>
                {currentPage + 1} / {lastPage + 1}
              </Text>
              <ActionIcon
                size={36}
                variant="default"
                aria-label="Next catalog page"
                disabled={currentPage === lastPage}
                onClick={() => setPage(currentPage + 1)}
              >
                <IconChevronRight size={16} />
              </ActionIcon>
            </Group>
          </Group>
        </>
      )}
    </Card>
  )
}

function CatalogModel({ id, providers }: { id: string; providers: string[] }) {
  const clipboard = useClipboard({ timeout: 2000 })
  return (
    <div className="catalog-model-entry">
      <Box miw={0}>
        <Text className="catalog-model-id" title={id}>{id}</Text>
        <Group gap={5} mt={7} wrap="wrap" aria-label="Available from providers">
          {providers.map((provider) => <Badge key={provider} size="xs" variant="light" color="gray">{provider}</Badge>)}
        </Group>
      </Box>
      <ActionIcon
        variant={clipboard.copied ? 'light' : 'subtle'}
        color={clipboard.copied ? 'teal' : 'gray'}
        size={40}
        aria-label={`Copy ${id}`}
        onClick={() => clipboard.copy(id)}
        style={{ flexShrink: 0 }}
      >
        {clipboard.copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
      </ActionIcon>
      <span className="visually-hidden" role="status">
        {clipboard.copied ? `${id} copied` : ''}
      </span>
    </div>
  )
}
