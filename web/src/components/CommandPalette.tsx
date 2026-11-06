import { Box, Group, Modal, ScrollArea, Text, UnstyledButton } from '@mantine/core'
import { motion } from 'framer-motion'
import { useMotionPreset, springSnappy } from '../motion/tokens'
import {
  IconBook, IconChartBar, IconKeyboard, IconSearch, IconTerminal2,
} from '@tabler/icons-react'
import { useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import { useNavigate } from 'react-router-dom'
import { NAV } from '../nav'

export interface PaletteItem {
  id: string
  group: 'Pages' | 'Resources' | 'Actions'
  label: string
  hint?: string
  icon: ComponentType<{ size?: number | string; stroke?: number }>
  keywords?: string
  run: () => void
}

// ⌘K command palette: a keyboard-first launcher for the four pages plus the
// raw-data resources and the shortcut cheat sheet. Fuzzy-ish filtering is
// substring-based on purpose — it is predictable, and operators type exact
// names (model IDs, provider names) far more often than prose.
export default function CommandPalette({
  opened,
  onClose,
  onShowShortcuts,
}: {
  opened: boolean
  onClose: () => void
  onShowShortcuts: () => void
}) {
  const navigate = useNavigate()
  const { reduced } = useMotionPreset()
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const items = useMemo<PaletteItem[]>(() => [
    ...NAV.map((item) => ({
      id: `nav-${item.path}`,
      group: 'Pages' as const,
      label: item.label,
      hint: item.description,
      icon: item.icon,
      keywords: `${item.label} ${item.description} ${item.path}`,
      run: () => navigate(item.path),
    })),
    {
      id: 'stats-json', group: 'Resources', label: 'Statistics JSON',
      hint: 'Per-model summary as JSON', icon: IconChartBar, keywords: 'json stats api',
      run: () => { window.location.href = '/stats' },
    },
    {
      id: 'metrics', group: 'Resources', label: 'Prometheus metrics',
      hint: 'Scrape endpoint', icon: IconBook, keywords: 'prometheus metrics scrape',
      run: () => { window.location.href = '/metrics' },
    },
    {
      id: 'shortcuts', group: 'Actions', label: 'Keyboard shortcuts',
      hint: 'Show the shortcut cheat sheet', icon: IconKeyboard, keywords: 'help keys shortcuts',
      run: onShowShortcuts,
    },
  ], [navigate, onShowShortcuts])

  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return items
    return items.filter((item) => `${item.label} ${item.hint ?? ''} ${item.keywords ?? ''}`.toLowerCase().includes(needle))
  }, [items, query])

  // Reset the highlight when the query narrows the list. Done in the change
  // handler instead of an effect so it happens in the same render pass.
  const changeQuery = (value: string) => {
    setQuery(value)
    setCursor(0)
  }

  // A fresh palette every time it opens: no stale query or highlight.
  useEffect(() => {
    if (!opened) return
    const id = window.setTimeout(() => {
      setQuery('')
      setCursor(0)
    }, 0)
    return () => window.clearTimeout(id)
  }, [opened])

  useEffect(() => {
    if (!opened) return
    const id = window.setTimeout(() => inputRef.current?.focus(), 60)
    return () => window.clearTimeout(id)
  }, [opened])

  useEffect(() => {
    const selected = listRef.current?.querySelector<HTMLElement>('[data-selected="true"]')
    selected?.scrollIntoView({ block: 'nearest' })
  }, [cursor, matches.length])

  const run = (item: PaletteItem) => {
    onClose()
    item.run()
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setCursor((value) => Math.min(value + 1, matches.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setCursor((value) => Math.max(value - 1, 0))
    } else if (event.key === 'Enter' && matches[cursor]) {
      event.preventDefault()
      run(matches[cursor])
    }
  }

  let lastGroup = ''
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      withCloseButton={false}
      size={560}
      radius="lg"
      padding={0}
      overlayProps={{ backgroundOpacity: 0.35, blur: reduced ? 0 : 2 }}
      transitionProps={{ transition: 'pop', duration: reduced ? 0 : 180 }}
      aria-label="Command menu"
    >
      <Box className="command-palette" onKeyDown={onKeyDown}>
        <Group gap={10} className="command-palette-input-row" wrap="nowrap">
          <IconSearch size={17} stroke={1.8} aria-hidden style={{ color: 'var(--muted)', flex: 'none' }} />
          <input
            ref={inputRef}
            className="command-palette-input"
            aria-label="Search commands and pages"
            placeholder="Jump to a page or resource…"
            value={query}
            onChange={(event) => changeQuery(event.currentTarget.value)}
            autoComplete="off"
            spellCheck={false}
          />
          <span className="command-palette-kbd" aria-hidden>esc</span>
        </Group>
        <ScrollArea.Autosize mah={420} ref={listRef} className="command-palette-list">
          {matches.length === 0 ? (
            <Text size="sm" c="dimmed" p="lg" ta="center">No commands match that search.</Text>
          ) : (
            matches.map((item, index) => {
              const showGroup = item.group !== lastGroup
              lastGroup = item.group
              const selected = index === cursor
              return (
                <div key={item.id}>
                  {showGroup && <div className="command-palette-group">{item.group}</div>}
                  <motion.div
                    layout={!reduced}
                    transition={reduced ? { duration: 0 } : springSnappy}
                  >
                    <UnstyledButton
                      className="command-palette-item"
                      data-selected={selected || undefined}
                      onMouseMove={() => setCursor(index)}
                      onClick={() => run(item)}
                    >
                      <span className="command-palette-item-icon" aria-hidden><item.icon size={17} stroke={1.7} /></span>
                      <span className="command-palette-item-copy">
                        <span className="command-palette-item-label">{item.label}</span>
                        {item.hint && <span className="command-palette-item-hint">{item.hint}</span>}
                      </span>
                      {selected && <span className="command-palette-enter" aria-hidden>↵</span>}
                    </UnstyledButton>
                  </motion.div>
                </div>
              )
            })
          )}
        </ScrollArea.Autosize>
        <Group justify="space-between" className="command-palette-footer" gap="sm">
          <Group gap={10}>
            <span className="command-palette-hint"><kbd>↑</kbd><kbd>↓</kbd> move</span>
            <span className="command-palette-hint"><kbd>↵</kbd> open</span>
          </Group>
          <span className="command-palette-hint"><kbd>esc</kbd> close</span>
        </Group>
      </Box>
    </Modal>
  )
}

// Small trigger for the top bar; exported so App can keep the shell tidy.
export function CommandPaletteButton({ onClick }: { onClick: () => void }) {
  return (
    <UnstyledButton className="command-trigger" onClick={onClick} aria-label="Open command menu">
      <IconTerminal2 size={14} stroke={1.8} aria-hidden />
      <span className="command-trigger-text">Jump to…</span>
      <span className="command-trigger-kbd" aria-hidden>⌘K</span>
    </UnstyledButton>
  )
}
