import { useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Group,
  Loader,
  Select,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useClipboard, useMediaQuery } from '@mantine/hooks'
import { AnimatePresence, motion } from 'framer-motion'
import {
  IconCheck,
  IconChevronLeft,
  IconChevronRight,
  IconCopy,
  IconCube,
  IconSearchOff,
  IconX,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { plural } from '../../format'
import { dashboardQueries } from '../../queries'
import SearchInput from '../SearchInput'
import { EmptyState } from '../EmptyState'
import { springSnappy, useMotionPreset } from '../../motion/tokens'

// The available-model catalog is a scanning surface: 500+ ids, no traffic
// numbers, one action (copy). It is a responsive card grid — provider prefix
// de-emphasized, provider names as mono chips, and a toolbar that stays put
// while the grid scrolls past it.
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
    return [...entries]
      .map(([id, providers]) => ({ id, providers }))
      .sort((a, b) => a.id.localeCompare(b.id))
  }, [query.data])
  const needle = filter.trim().toLowerCase()
  const visible = catalog.filter(
    (entry) =>
      (provider === 'all' || entry.providers.includes(provider)) &&
      `${entry.id} ${entry.providers.join(' ')}`.toLowerCase().includes(needle),
  )
  const lastPage = Math.max(0, Math.ceil(visible.length / pageSize) - 1)
  const currentPage = Math.min(page, lastPage)
  const start = currentPage * pageSize + 1
  const end = Math.min((currentPage + 1) * pageSize, visible.length)
  const failed = enabled.filter((backend) => !backend.catalogOK).length
  const rangeLabel = visible.length
    ? `${start}–${end} of ${visible.length} available models`
    : '0 available models'

  function clearProvider() {
    setProvider('all')
    setPage(0)
  }
  function clearSearch() {
    setFilter('')
    setProvider('all')
    setPage(0)
  }

  return (
    <section className="catalog-catalog" aria-label="Available model catalog">
      <header className="catalog-catalog-head">
        <Group gap="sm" wrap="nowrap" miw={0} align="flex-start">
          <span className="catalog-section-mark" aria-hidden>
            <IconCube size={16} />
          </span>
          <Box miw={0}>
            <Title order={2} className="catalog-catalog-title">
              Available models
            </Title>
            <Text size="xs" c="dimmed" className="catalog-catalog-subtitle">
              Search the enabled provider catalogs, then copy the complete ID for your client.
            </Text>
          </Box>
        </Group>
        <Badge variant="light" color="gray" className="catalog-count-badge">
          {query.data ? plural(catalog.length, 'model') : '— models'}
        </Badge>
      </header>

      <div className="catalog-toolbar">
        <Group gap="sm" align="flex-start" wrap="nowrap" className="catalog-toolbar-row">
          <Box flex={1} miw={0}>
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
            aria-label="Filter available models by provider"
            className="catalog-provider-filter"
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
        <Group gap="xs" justify="space-between" wrap="wrap" className="catalog-toolbar-meta">
          <Group gap={6} wrap="wrap" miw={0} className="catalog-filter-chips">
            <Text size="xs" c="dimmed" aria-live="polite" className="catalog-result-count">
              {query.data ? `${visible.length} of ${catalog.length} models` : 'Loading models…'}
            </Text>
            {provider !== 'all' && (
              <FilterChip
                label={provider}
                clearLabel={`Clear provider filter ${provider}`}
                onClear={clearProvider}
              />
            )}
            {needle.length > 0 && (
              <FilterChip label={`“${filter.trim()}”`} clearLabel="Clear model search" onClear={clearSearch} />
            )}
          </Group>
          <Text size="xs" c="dimmed" className="catalog-toolbar-note">
            {failed > 0
              ? `${failed} provider catalog${failed === 1 ? ' is' : 's are'} unavailable`
              : plural(enabled.length, 'enabled provider')}
          </Text>
        </Group>
      </div>

      {query.isPending ? (
        <Group py="xl" justify="center" gap="xs" role="status" className="catalog-loading">
          <Loader size="sm" />
          <Text size="sm">Loading model catalog…</Text>
        </Group>
      ) : query.isError ? (
        <Alert mt="md" color="red" title="Model catalog unavailable">
          <Stack gap="xs" align="flex-start">
            <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{query.error.message}</Text>
            <Button variant="light" mih={44} onClick={() => query.refetch()}>
              Retry
            </Button>
          </Stack>
        </Alert>
      ) : (
        <>
          {visible.length === 0 ? (
            <EmptyState
              icon={<IconSearchOff size={24} />}
              title={catalog.length === 0 ? 'No provider models are available' : 'No models match that search'}
              hint={
                catalog.length === 0
                  ? 'Enable a provider with a reachable catalog, then reload this page.'
                  : 'Try another name, another provider, or clear the filters.'
              }
            />
          ) : (
            <CatalogGrid
              entries={visible.slice(currentPage * pageSize, end)}
              key={`${currentPage}-${provider}-${needle}`}
            />
          )}
          {visible.length === 0 && (needle.length > 0 || provider !== 'all') && (
            <Group justify="center">
              <Button
                variant="light"
                mih={44}
                aria-label="Clear search and provider filters"
                onClick={clearSearch}
              >
                Clear search
              </Button>
            </Group>
          )}
          {visible.length > 0 && (
            <Group className="catalog-pagination" justify="space-between" gap="xs">
              <Text size="xs" c="dimmed" aria-live="polite" className="tabular">
                {rangeLabel}
              </Text>
              <Group gap={4} wrap="nowrap">
                <ActionIcon
                  size={36}
                  variant="default"
                  aria-label="Previous catalog page"
                  disabled={currentPage === 0}
                  onClick={() => setPage(currentPage - 1)}
                >
                  <IconChevronLeft size={16} />
                </ActionIcon>
                <Text size="xs" c="dimmed" px={6} className="tabular">
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
          )}
        </>
      )}
    </section>
  )
}

function CatalogGrid({
  entries,
}: {
  entries: { id: string; providers: string[] }[]
}) {
  const { stagger, child } = useMotionPreset()
  return (
    <motion.div
      className="catalog-model-grid"
      variants={stagger}
      initial={stagger ? 'hidden' : false}
      animate={stagger ? 'visible' : undefined}
    >
      {entries.map((entry) => (
        <motion.div key={entry.id} className="catalog-model-cell" variants={child}>
          <CatalogModel id={entry.id} providers={entry.providers} />
        </motion.div>
      ))}
    </motion.div>
  )
}

function CatalogModel({ id, providers }: { id: string; providers: string[] }) {
  const clipboard = useClipboard({ timeout: 2000 })
  const { transition, reduced } = useMotionPreset()
  const slash = id.indexOf('/')
  const namespace = slash >= 0 ? id.slice(0, slash + 1) : ''
  const name = slash >= 0 ? id.slice(slash + 1) : id
  return (
    <div className="catalog-model-entry">
      <div className="catalog-model-entry-body">
        <Text className="catalog-model-id" title={id}>
          {namespace && <span className="catalog-model-prefix">{namespace}</span>}
          <span className="catalog-model-name-part">{name}</span>
        </Text>
        <Group gap={5} wrap="wrap" className="catalog-model-providers" aria-label="Available from providers">
          {providers.map((name) => (
            <Badge key={name} className="catalog-provider-chip" variant="light" color="gray">
              {name}
            </Badge>
          ))}
        </Group>
      </div>
      <ActionIcon
        className="catalog-model-copy"
        variant={clipboard.copied ? 'light' : 'default'}
        color={clipboard.copied ? 'teal' : 'gray'}
        size={40}
        aria-label={`Copy ${id}`}
        title={clipboard.copied ? 'Copied' : `Copy ${id}`}
        onClick={() => clipboard.copy(id)}
      >
        <AnimatePresence initial={false} mode="wait">
          <motion.span
            key={clipboard.copied ? 'check' : 'copy'}
            initial={reduced ? false : { scale: 0.4, rotate: -30, opacity: 0 }}
            animate={{ scale: 1, rotate: 0, opacity: 1 }}
            exit={reduced ? undefined : { scale: 0.4, rotate: 30, opacity: 0 }}
            transition={transition(springSnappy)}
            style={{ display: 'grid', placeItems: 'center' }}
          >
            {clipboard.copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
          </motion.span>
        </AnimatePresence>
      </ActionIcon>
      <span className="visually-hidden" role="status">
        {clipboard.copied ? `${id} copied` : ''}
      </span>
    </div>
  )
}

function FilterChip({
  label,
  clearLabel,
  onClear,
}: {
  label: string
  clearLabel: string
  onClear: () => void
}) {
  return (
    <span className="catalog-filter-chip">
      <span className="catalog-filter-chip-label">{label}</span>
      <ActionIcon
        size={18}
        radius="xl"
        variant="transparent"
        color="gray"
        aria-label={clearLabel}
        onClick={onClear}
      >
        <IconX size={12} stroke={2.4} />
      </ActionIcon>
    </span>
  )
}
