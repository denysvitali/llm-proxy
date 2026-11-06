import {
  ActionIcon, Anchor, AppShell, Badge, Box, Group, Kbd, Modal,
  type MantineColorScheme, NavLink as MantineNavLink, SegmentedControl, type SegmentedControlItem,
  Skeleton, Stack, Text, Tooltip, UnstyledButton, useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import {
  IconActivity, IconBook, IconChartBar, IconDeviceDesktop, IconKeyboard,
  IconMoonStars, IconPlugConnected, IconSearch, IconSun,
} from '@tabler/icons-react'
import { motion } from 'framer-motion'
import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useState } from 'react'
import { NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { dashboardQueries } from './queries'
import { useLiveStatsUpdates } from './useLiveUpdates'
import { NAV, isActiveNavPath } from './nav'
import { PageErrorBoundary } from './components/PageErrorBoundary'
import Fade from './components/Fade'
import RouteProgress from './components/RouteProgress'
import CommandPalette, { CommandPaletteButton } from './components/CommandPalette'
import { springGentle, useMotionPreset } from './motion/tokens'
import './app-shell.css'

export { Fade }

const OverviewPage = lazy(() => import('./pages/Overview'))
const ModelsPage = lazy(() => import('./pages/Models'))
const ProvidersPage = lazy(() => import('./pages/Providers'))
const SetupPage = lazy(() => import('./pages/Setup'))

const colorSchemeData: SegmentedControlItem<MantineColorScheme>[] = [
  { value: 'light', label: <IconSun size={15} aria-label="Light" /> },
  { value: 'auto', label: <IconDeviceDesktop size={15} aria-label="Auto" /> },
  { value: 'dark', label: <IconMoonStars size={15} aria-label="Dark" /> },
]

const SHORTCUTS = [
  { keys: 'g then o', action: 'Go to Overview' },
  { keys: 'g then m', action: 'Go to Models' },
  { keys: 'g then p', action: 'Go to Providers' },
  { keys: 'g then s', action: 'Go to Setup' },
  { keys: '⌘K', action: 'Open the command menu' },
  { keys: '?', action: 'Show this help' },
]

export default function App() {
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const pageName = NAV.find((item) => isActiveNavPath(pathname, item.path))?.label ?? 'Overview'
  const [shortcutsOpen, setShortcutsOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const { reduced } = useMotionPreset()

  // Focus main content on route change
  useEffect(() => {
    document.getElementById('main')?.focus()
  }, [pathname])

  // Keyboard shortcuts
  useEffect(() => {
    let lastGPress = 0
    function handleKeyDown(e: KeyboardEvent) {
      const target = e.target instanceof HTMLElement ? e.target : null
      if (e.defaultPrevented || e.isComposing || e.repeat || e.altKey
        || shortcutsOpen || paletteOpen || target?.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="dialog"]')) {
        lastGPress = 0
        return
      }

      // ⌘K / Ctrl+K opens the command menu from anywhere in the shell.
      if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        lastGPress = 0
        setPaletteOpen(true)
        return
      }

      if (e.key === 'g' && !e.metaKey && !e.ctrlKey) {
        lastGPress = Date.now()
        return
      }

      if (e.key === '?' && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        lastGPress = 0
        setShortcutsOpen(true)
        return
      }

      const isNavigationSequence = Date.now() - lastGPress < 1000 && !e.metaKey && !e.ctrlKey
      lastGPress = 0
      if (!isNavigationSequence) return

      switch (e.key) {
        case 'o': navigate('/'); break
        case 'm': navigate('/models'); break
        case 'p': navigate('/providers'); break
        case 's': navigate('/setup'); break
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [navigate, shortcutsOpen, paletteOpen])

  return (
    <AppShell
      layout="alt"
      header={{ height: isMobile ? 52 : 60 }}
      navbar={isMobile ? undefined : { width: 244, breakpoint: 'sm' }}
      footer={isMobile ? { height: 'calc(66px + env(safe-area-inset-bottom, 0px))' } : { height: 0, collapsed: true }}
      padding={0}
    >
      <Anchor href="#main" className="app-skip-link">Skip to content</Anchor>
      <RouteProgress routeKey={pathname} />
      <AppShell.Header className="app-topbar" withBorder={false}>
        <div className="topbar-inner">
          <HeaderBrand subtitle={isMobile ? pageName : undefined} />
          <div className="topbar-actions">
            {!isMobile && <CommandPaletteButton onClick={() => setPaletteOpen(true)} />}
            <LiveStatusBadge />
            {isMobile && (
              <ActionIcon variant="subtle" size={34} aria-label="Open command menu" onClick={() => setPaletteOpen(true)}>
                <IconSearch size={17} aria-hidden />
              </ActionIcon>
            )}
            <Tooltip label="Keyboard shortcuts">
              <ActionIcon variant="subtle" size={34} aria-label="Keyboard shortcuts" onClick={() => setShortcutsOpen(true)}>
                <IconKeyboard size={17} aria-hidden />
              </ActionIcon>
            </Tooltip>
            <ColorSchemeToggle />
          </div>
        </div>
      </AppShell.Header>

      {!isMobile && (
        <AppShell.Navbar withBorder={false}>
          <SidebarNavigation />
        </AppShell.Navbar>
      )}

      <AppShell.Main id="main" tabIndex={-1}>
        <div className="page-container">
          <motion.div
            key={pathname}
            initial={reduced ? false : { opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={reduced ? { duration: 0 } : springGentle}
            className="page-transition"
          >
              <Suspense fallback={<PageLoading name={pageName} />}>
                <PageErrorBoundary key={pathname}>
                  <Routes>
                    <Route path="/" element={<OverviewPage />} />
                    <Route path="/models" element={<ModelsPage />} />
                    <Route path="/providers" element={<ProvidersPage />} />
                    <Route path="/setup" element={<SetupPage />} />
                    <Route path="*" element={<OverviewPage />} />
                  </Routes>
                </PageErrorBoundary>
              </Suspense>
            </motion.div>
          <Group className="app-footer" justify="space-between" gap="sm" mt="xl">
            <Text size="xs" c="dimmed">llm-proxy · model gateway</Text>
            <Group gap="md">
              <Anchor href="/stats" size="xs" c="dimmed">Statistics JSON</Anchor>
              <Anchor href="/metrics" size="xs" c="dimmed">Prometheus metrics</Anchor>
            </Group>
          </Group>
        </div>
      </AppShell.Main>

      {isMobile && (
        <AppShell.Footer withBorder={false}>
          <Navigation mobile />
        </AppShell.Footer>
      )}

      <CommandPalette
        opened={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        onShowShortcuts={() => setShortcutsOpen(true)}
      />

      <Modal opened={shortcutsOpen} onClose={() => setShortcutsOpen(false)} title="Keyboard shortcuts" centered radius="lg">
        <Text size="sm" c="dimmed" mb="lg">Move around your workspace without leaving the keyboard.</Text>
        <Stack gap={2}>
          {SHORTCUTS.map(({ keys, action }) => (
            <Group key={keys} justify="space-between" gap="md" className="shortcut-row">
              <Group gap={5}>{keys.split(' then ').map((key, index) => <span key={key}>{index > 0 && <Text component="span" size="xs" c="dimmed" mr={5}>then</Text>}<Kbd>{key}</Kbd></span>)}</Group>
              <Text size="sm" c="dimmed">{action}</Text>
            </Group>
          ))}
        </Stack>
      </Modal>
    </AppShell>
  )
}

function PageLoading({ name }: { name: string }) {
  return (
    <Stack className="page-loading" gap="lg" role="status" aria-label={`Loading ${name.toLowerCase()}`}>
      <Skeleton height={24} width={200} radius="sm" />
      <Skeleton height={132} radius="lg" />
      <Skeleton height={220} radius="lg" />
    </Stack>
  )
}

function HeaderBrand({ subtitle }: { subtitle?: string }) {
  const { data } = useQuery(dashboardQueries.overview())
  return (
    <UnstyledButton component={NavLink} to="/" className="brand-link" aria-label="llm-proxy home">
      <span className="brand-mark" aria-hidden>
        <IconActivity size={16} stroke={2.2} />
      </span>
      <span className="brand-text">
        <strong>llm-proxy</strong>
        <small>{subtitle ?? (data?.version ? `v${data.version} · model gateway` : 'model gateway')}</small>
      </span>
    </UnstyledButton>
  )
}

function LiveStatusBadge() {
  const { connected, live } = useLiveStatsUpdates()
  return (
    <Tooltip label={connected ? 'Receiving live traffic updates; every request blips this indicator' : 'Live updates disconnected; periodic refresh remains available'}>
      <Badge variant="light" color={connected ? 'teal' : 'gray'} tt="none" className="live-status" data-connected={connected} aria-live="polite">
        {/* Remount on each live request to restart the blip animation. */}
        <span key={live?.count ?? 'idle'} className="live-led" data-pulse={live ? 'on' : 'off'} aria-hidden="true" />
        <span className="live-status-text">{connected ? 'Live' : 'Offline'}</span>
      </Badge>
    </Tooltip>
  )
}

function Navigation({ mobile = false }: { mobile?: boolean }) {
  const { pathname } = useLocation()
  return (
    <Box component="nav" aria-label="Main navigation" className={mobile ? 'bottom-navigation' : 'sidebar-nav'}>
      {NAV.map(({ path, label, icon: Icon }) => {
        const active = isActiveNavPath(pathname, path)
        return (
          <UnstyledButton key={path} component={NavLink} to={path}
            className={mobile ? 'bottom-nav-link' : 'sidebar-nav-link'}
            data-active={active || undefined}
            aria-label={label}
            aria-current={active ? 'page' : undefined}>
            <span className="nav-icon-wrap">
              <Icon size={mobile ? 19 : 18} stroke={1.75} aria-hidden />
            </span>
            <span className="nav-label">{label}</span>
          </UnstyledButton>
        )
      })}
    </Box>
  )
}

function SidebarNavigation() {
  const { pathname } = useLocation()
  const { data } = useQuery(dashboardQueries.overview())
  return (
    <Box component="nav" aria-label="Main navigation" className="sidebar-nav">
      <Box className="sidebar-caption" aria-hidden="true">Workspace</Box>
      {NAV.map(({ path, label, description, icon: Icon }) => (
        <MantineNavLink
          key={path}
          component={NavLink}
          to={path}
          label={label}
          description={description}
          leftSection={<Icon size={18} stroke={1.75} aria-hidden />}
          active={isActiveNavPath(pathname, path)}
          aria-label={label}
          aria-current={isActiveNavPath(pathname, path) ? 'page' : undefined}
          className="sidebar-nav-link"
        />
      ))}
      <Box className="sidebar-caption" aria-hidden="true">Resources</Box>
      <MantineNavLink component="a" href="/stats" label="Statistics JSON" leftSection={<IconChartBar size={17} stroke={1.75} aria-hidden />} className="sidebar-nav-link" />
      <MantineNavLink component="a" href="/metrics" label="Prometheus metrics" leftSection={<IconBook size={17} stroke={1.75} aria-hidden />} className="sidebar-nav-link" />
      <MantineNavLink component={NavLink} to="/setup" label="Connect a client" leftSection={<IconPlugConnected size={17} stroke={1.75} aria-hidden />} className="sidebar-nav-link" />
      <Box className="sidebar-footer">
        <span className="sidebar-health" aria-hidden data-on={data ? 'true' : 'false'} />
        <Text size="xs" c="dimmed" truncate>
          {data?.version ? `v${data.version}` : 'llm-proxy'}
          {data?.authEnabled ? ' · auth on' : ' · auth off'}
        </Text>
      </Box>
    </Box>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  return <SegmentedControl className="color-scheme-control" size="xs" aria-label="Color scheme" data={colorSchemeData} value={colorScheme} onChange={setColorScheme} />
}
