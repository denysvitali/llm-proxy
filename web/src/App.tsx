import {
  ActionIcon, Anchor, AppShell, Badge, Box, Group, Kbd, Modal,
  type MantineColorScheme, NavLink as MantineNavLink, SegmentedControl, type SegmentedControlItem,
  Skeleton, Stack, Text, Tooltip, UnstyledButton, useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import {
  IconActivity, IconBook, IconChartBar, IconDeviceDesktop, IconKeyboard,
  IconMoonStars, IconPlugConnected, IconSun, IconTerminal2,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useState } from 'react'
import { NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { dashboardQueries } from './queries'
import { useLiveStatsUpdates } from './useLiveUpdates'
import { NAV, isActiveNavPath } from './nav'
import { PageErrorBoundary } from './components/PageErrorBoundary'
import Fade from './components/Fade'

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
  { keys: '?', action: 'Show this help' },
]

export default function App() {
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const pageName = NAV.find((item) => isActiveNavPath(pathname, item.path))?.label ?? 'Overview'
  const [shortcutsOpen, setShortcutsOpen] = useState(false)

  // Focus main content on route change
  useEffect(() => {
    document.getElementById('main')?.focus()
  }, [pathname])

  // Keyboard shortcuts
  useEffect(() => {
    let lastGPress = 0
    function handleKeyDown(e: KeyboardEvent) {
      const target = e.target instanceof HTMLElement ? e.target : null
      if (e.defaultPrevented || e.isComposing || e.repeat || e.ctrlKey || e.metaKey || e.altKey
        || shortcutsOpen || target?.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="dialog"]')) {
        lastGPress = 0
        return
      }

      if (e.key === 'g') {
        lastGPress = Date.now()
        return
      }

      if (e.key === '?') {
        e.preventDefault()
        lastGPress = 0
        setShortcutsOpen(true)
        return
      }

      const isNavigationSequence = Date.now() - lastGPress < 1000
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
  }, [navigate, shortcutsOpen])

  return (
    <AppShell
      layout="alt"
      header={{ height: isMobile ? 50 : 58 }}
      navbar={isMobile ? undefined : { width: 248, breakpoint: 'sm' }}
      footer={isMobile ? { height: 'calc(62px + env(safe-area-inset-bottom, 0px))' } : { height: 0, collapsed: true }}
      padding={0}
    >
      <Anchor href="#main" className="app-skip-link">Skip to content</Anchor>
      <AppShell.Header className="app-topbar" withBorder={false}>
        <div className="topbar-inner">
          <HeaderBrand subtitle={isMobile ? pageName : undefined} />
          <div className="topbar-actions">
            <LiveStatusBadge />
            <Tooltip label="Keyboard shortcuts">
              <ActionIcon variant="default" size={34} aria-label="Keyboard shortcuts" onClick={() => setShortcutsOpen(true)}>
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

      <Modal opened={shortcutsOpen} onClose={() => setShortcutsOpen(false)} title="Keyboard shortcuts" centered>
        <Text size="sm" c="dimmed" mb="lg">Move around your workspace without leaving the keyboard.</Text>
        <Stack gap="sm">
          {SHORTCUTS.map(({ keys, action }) => (
            <Group key={keys} justify="space-between" gap="md">
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
      {NAV.map(({ path, label, icon: Icon }) => (
        <UnstyledButton key={path} component={NavLink} to={path}
          className={mobile ? 'bottom-nav-link' : 'sidebar-nav-link'}
          data-active={isActiveNavPath(pathname, path) || undefined}
          aria-label={label}
          aria-current={isActiveNavPath(pathname, path) ? 'page' : undefined}>
          <Icon size={mobile ? 20 : 18} stroke={1.75} aria-hidden />
          <span className="nav-label">{label}</span>
        </UnstyledButton>
      ))}
    </Box>
  )
}

function SidebarNavigation() {
  const { pathname } = useLocation()
  const { data } = useQuery(dashboardQueries.overview())
  return (
    <Box component="nav" aria-label="Main navigation" className="sidebar-nav">
      <Box className="sidebar-caption" aria-hidden="true">Gateway</Box>
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
        />
      ))}
      <Box className="sidebar-caption" aria-hidden="true">Resources</Box>
      <MantineNavLink component="a" href="/stats" label="Statistics JSON" leftSection={<IconChartBar size={17} stroke={1.75} aria-hidden />} />
      <MantineNavLink component="a" href="/metrics" label="Prometheus metrics" leftSection={<IconBook size={17} stroke={1.75} aria-hidden />} />
      <MantineNavLink component={NavLink} to="/setup" label="Connect a client" leftSection={<IconPlugConnected size={17} stroke={1.75} aria-hidden />} />
      <Box className="sidebar-footer">
        <Group gap={7} wrap="nowrap">
          <IconTerminal2 size={13} aria-hidden style={{ color: 'var(--muted)', flex: 'none' }} />
          <Text size="xs" c="dimmed" truncate>
            {data?.version ? `llm-proxy v${data.version}` : 'llm-proxy'}
            {data?.authEnabled ? ' · auth on' : ' · auth off'}
          </Text>
        </Group>
      </Box>
    </Box>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  return <SegmentedControl className="color-scheme-control" size="xs" aria-label="Color scheme" data={colorSchemeData} value={colorScheme} onChange={setColorScheme} />
}
