import {
  Anchor, AppShell, Badge, Box, Container, Group, Loader, Modal,
  type MantineColorScheme, SegmentedControl, type SegmentedControlItem,
  Stack, Text, Tooltip, UnstyledButton, useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconArrowUpRight, IconDeviceDesktop, IconMoonStars, IconSun } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { fetchOverview } from './api'
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
  { value: 'light', label: <IconSun size={16} aria-label="Light" /> },
  { value: 'auto', label: <IconDeviceDesktop size={16} aria-label="Auto" /> },
  { value: 'dark', label: <IconMoonStars size={16} aria-label="Dark" /> },
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
  const lastGPress = useRef(0)
  const gTimeout = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  // Focus main content on route change
  useEffect(() => {
    document.getElementById('main')?.focus()
  }, [pathname])

  // Keyboard shortcuts
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return

      if (e.key === 'g') {
        lastGPress.current = Date.now()
        if (gTimeout.current) clearTimeout(gTimeout.current)
        gTimeout.current = setTimeout(() => { lastGPress.current = 0 }, 1000)
        return
      }

      if (e.key === '?' || (e.key === '/' && e.shiftKey)) {
        setShortcutsOpen(true)
        return
      }

      if (Date.now() - lastGPress.current > 1000) return

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
      if (gTimeout.current) clearTimeout(gTimeout.current)
    }
  }, [navigate])

  return (
    <AppShell
      layout="alt"
      header={{ height: 64 }}
      navbar={{ width: 216, breakpoint: 'sm', collapsed: { mobile: true, desktop: isMobile } }}
      footer={isMobile ? { height: 'calc(64px + env(safe-area-inset-bottom, 0px))' } : { height: 0, collapsed: true }}
      padding={0}
    >
      <Anchor href="#main" className="app-skip-link">Skip to content</Anchor>
      {!isMobile && (
        <AppShell.Navbar component="aside" className="app-sidebar" p="md">
          <HeaderBrand />
          <Text className="nav-caption" mt={42} mb="sm">Workspace</Text>
          <Navigation />
          <Box mt="auto" className="sidebar-note">
            <Text size="sm" fw={600}>Connect your tools</Text>
            <Text size="xs" c="dimmed" mt={6}>Use one endpoint for your coding agents and API clients.</Text>
            <Anchor component={NavLink} to="/setup" size="xs" mt="md" className="setup-link">
              Connect an agent <IconArrowUpRight size={14} />
            </Anchor>
          </Box>
        </AppShell.Navbar>
      )}
      <AppShell.Header>
        <Group h="100%" justify="space-between" px={{ base: 'md', sm: 'xl' }} wrap="nowrap">
          {isMobile ? <HeaderBrand /> : (
            <Group gap="sm" className="header-breadcrumb">
              <Text size="sm" c="dimmed">Workspace</Text>
              <Text size="sm" c="dimmed" aria-hidden>/</Text>
              <Text size="sm" fw={600}>{pageName}</Text>
            </Group>
          )}
          <Group gap="md" wrap="nowrap">
            <LiveStatusBadge />
            <ColorSchemeToggle />
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main id="main" tabIndex={-1}>
        <Container size={1600} className="page-container">
          <Suspense fallback={<Group justify="center" py="xl"><Loader size="sm" /></Group>}>
            <PageErrorBoundary>
              <Routes>
                <Route path="/" element={<OverviewPage />} />
                <Route path="/models" element={<ModelsPage />} />
                <Route path="/providers" element={<ProvidersPage />} />
                <Route path="/setup" element={<SetupPage />} />
                <Route path="*" element={<OverviewPage />} />
              </Routes>
            </PageErrorBoundary>
          </Suspense>
          <Group className="app-footer" justify="space-between" gap="sm" mt={40}>
            <Text size="xs" c="dimmed">llm-proxy · Your model gateway</Text>
            <Group gap="md">
              <Anchor href="/stats" size="xs" c="dimmed">Statistics JSON</Anchor>
              <Anchor href="/metrics" size="xs" c="dimmed">Prometheus metrics</Anchor>
            </Group>
          </Group>
        </Container>
      </AppShell.Main>
      {isMobile && <AppShell.Footer><Navigation mobile /></AppShell.Footer>}
      <Modal opened={shortcutsOpen} onClose={() => setShortcutsOpen(false)} title="Keyboard shortcuts">
        <Stack gap="sm">
          {SHORTCUTS.map(({ keys, action }) => (
            <Group key={keys} justify="space-between" gap="md">
              <Text size="sm" fw={500} ff="monospace">{keys}</Text>
              <Text size="sm" c="dimmed">{action}</Text>
            </Group>
          ))}
        </Stack>
      </Modal>
    </AppShell>
  )
}

function HeaderBrand() {
  const { data } = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  return (
    <UnstyledButton component={NavLink} to="/" className="brand-link" aria-label="llm-proxy home">
      <span className="brand-mark" aria-hidden>λ</span>
      <Stack gap={1}>
        <Text fw={750} size="md" lh={1.2} lts="-0.03em">llm-proxy</Text>
        <Text c="dimmed" fz={11} lh={1.2}>{data?.version ? `Gateway / v${data.version}` : 'Model gateway'}</Text>
      </Stack>
    </UnstyledButton>
  )
}

function LiveStatusBadge() {
  const connected = useLiveStatsUpdates()
  return (
    <Tooltip label={connected ? 'Receiving live traffic updates' : 'Live updates disconnected; stats refresh on page load'}>
      <Badge variant="dot" color={connected ? 'teal' : 'gray'} tt="none" className="live-status" aria-live="polite">
        <span className="live-status-text">{connected ? 'Live' : 'Offline'}</span>
      </Badge>
    </Tooltip>
  )
}

function Navigation({ mobile = false }: { mobile?: boolean }) {
  const { pathname } = useLocation()
  return (
    <Box component="nav" aria-label="Main navigation" className={mobile ? 'bottom-navigation' : 'side-navigation'}>
      {NAV.map(({ path, label, icon: Icon }) => (
        <UnstyledButton key={path} component={NavLink} to={path}
          className={mobile ? 'bottom-nav-link' : 'side-nav-link'}
          data-active={isActiveNavPath(pathname, path) || undefined}
          aria-current={isActiveNavPath(pathname, path) ? 'page' : undefined}>
          <Icon size={20} stroke={1.7} aria-hidden />
          <span>{label}</span>
        </UnstyledButton>
      ))}
    </Box>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  return <SegmentedControl size="xs" aria-label="Color scheme" data={colorSchemeData} value={colorScheme} onChange={setColorScheme} />
}
