import {
  Anchor, AppShell, Badge, Box, Container, Group, Kbd, Modal,
  type MantineColorScheme, SegmentedControl, type SegmentedControlItem,
  Skeleton, Stack, Text, Tooltip, UnstyledButton, useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconArrowUpRight, IconDeviceDesktop, IconKeyboard, IconMoonStars, IconSun, IconTerminal2 } from '@tabler/icons-react'
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
      header={{ height: 60 }}
      navbar={{ width: 232, breakpoint: 'sm', collapsed: { mobile: true, desktop: isMobile } }}
      footer={isMobile ? { height: 'calc(64px + env(safe-area-inset-bottom, 0px))' } : { height: 0, collapsed: true }}
      padding={0}
    >
      <Anchor href="#main" className="app-skip-link">Skip to content</Anchor>
      {!isMobile && (
        <AppShell.Navbar component="aside" className="app-sidebar" p="md">
          <HeaderBrand />
          <Text className="nav-caption" mt={36} mb="sm">Gateway workspace</Text>
          <Navigation />
          <Stack mt="auto" gap="md" pt="xl">
            <Anchor component={NavLink} to="/setup" className="sidebar-connect" underline="never">
              <IconTerminal2 size={18} aria-hidden />
              <span><strong>Connect a client</strong><span>Endpoints & examples</span></span>
              <IconArrowUpRight size={15} aria-hidden />
            </Anchor>
            <UnstyledButton className="shortcut-trigger" onClick={() => setShortcutsOpen(true)}>
              <IconKeyboard size={17} aria-hidden />
              <span>Keyboard shortcuts</span>
              <kbd>?</kbd>
            </UnstyledButton>
          </Stack>
        </AppShell.Navbar>
      )}
      <AppShell.Header>
        <Group h="100%" justify="space-between" px={{ base: 'md', sm: 'xl' }} wrap="nowrap">
          {isMobile ? <HeaderBrand subtitle={pageName} /> : (
            <Group gap="sm" className="header-breadcrumb">
              <span className="header-workspace-dot" aria-hidden />
              <Text size="sm" c="dimmed">Gateway</Text>
              <Text size="sm" c="dimmed" aria-hidden>/</Text>
              <Text size="sm" fw={600}>{pageName}</Text>
            </Group>
          )}
          <Group gap="md" wrap="nowrap">
            <LiveStatusBadge />
            {!isMobile && <Anchor component={NavLink} to="/setup" className="header-connect" size="sm" underline="never"><IconTerminal2 size={16} aria-hidden /> Connect client</Anchor>}
            <ColorSchemeToggle />
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main id="main" tabIndex={-1}>
        <Container size={1600} className="page-container">
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
          <Group className="app-footer" justify="space-between" gap="sm" mt={40}>
            <Text size="xs" c="dimmed">llm-proxy <span className="footer-divider" aria-hidden>/</span> Model gateway</Text>
            <Group gap="md">
              <Anchor href="/stats" size="xs" c="dimmed">Statistics JSON</Anchor>
              <Anchor href="/metrics" size="xs" c="dimmed">Prometheus metrics</Anchor>
            </Group>
          </Group>
        </Container>
      </AppShell.Main>
      {isMobile && <AppShell.Footer><Navigation mobile /></AppShell.Footer>}
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
      <Stack gap="sm"><Skeleton height={36} width={180} /><Skeleton height={16} width="min(100%, 380px)" /></Stack>
      <div className="page-loading-metrics">{Array.from({ length: 4 }, (_, index) => <Skeleton key={index} height={120} radius="lg" />)}</div>
      <Skeleton height={260} radius="lg" />
    </Stack>
  )
}

function HeaderBrand({ subtitle }: { subtitle?: string }) {
  const { data } = useQuery(dashboardQueries.overview())
  return (
    <UnstyledButton component={NavLink} to="/" className="brand-link" aria-label="llm-proxy home">
      <span className="brand-mark" aria-hidden>λ</span>
      <Stack gap={1}>
        <Text fw={700} size="lg" lh={1.2} lts="-0.03em">llm-proxy</Text>
        <Text c="dimmed" fz={11} lh={1.4}>{subtitle ?? (data?.version ? `Model gateway · v${data.version}` : 'Model gateway')}</Text>
      </Stack>
    </UnstyledButton>
  )
}

function LiveStatusBadge() {
  const connected = useLiveStatsUpdates()
  return (
    <Tooltip label={connected ? 'Receiving live traffic updates' : 'Live updates disconnected; periodic refresh remains available'}>
      <Badge variant="dot" color={connected ? 'teal' : 'gray'} tt="none" className="live-status" aria-live="polite">
        <span className="live-status-text">{connected ? 'Live updates' : 'Updates offline'}</span>
      </Badge>
    </Tooltip>
  )
}

function Navigation({ mobile = false }: { mobile?: boolean }) {
  const { pathname } = useLocation()
  return (
    <Box component="nav" aria-label="Main navigation" className={mobile ? 'bottom-navigation' : 'side-navigation'}>
      {NAV.map(({ path, label, description, icon: Icon }) => (
        <UnstyledButton key={path} component={NavLink} to={path}
          className={mobile ? 'bottom-nav-link' : 'side-nav-link'}
          data-active={isActiveNavPath(pathname, path) || undefined}
          aria-label={label}
          title={description}
          aria-current={isActiveNavPath(pathname, path) ? 'page' : undefined}>
          <span className="nav-icon"><Icon size={20} stroke={1.7} aria-hidden /></span>
          <span className="nav-label"><span>{label}</span>{!mobile && <span className="nav-description">{description}</span>}</span>
        </UnstyledButton>
      ))}
    </Box>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  return <SegmentedControl className="color-scheme-control" size="xs" aria-label="Color scheme" data={colorSchemeData} value={colorScheme} onChange={setColorScheme} />
}
