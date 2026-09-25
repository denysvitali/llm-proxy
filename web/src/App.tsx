import {
  Anchor, AppShell, Badge, Box, Container, Group, type MantineColorScheme,
  SegmentedControl, type SegmentedControlItem, Stack, Text, Tooltip,
  UnstyledButton, useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconArrowUpRight, IconDeviceDesktop, IconMoonStars, IconSun } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { NavLink, Route, Routes, useLocation } from 'react-router-dom'
import { fetchOverview } from './api'
import { useLiveStatsUpdates } from './useLiveUpdates'
import { NAV, isActiveNavPath } from './nav'
import { PageErrorBoundary } from './components/PageErrorBoundary'
import OverviewPage from './pages/Overview'
import ModelsPage from './pages/Models'
import ProvidersPage from './pages/Providers'
import SetupPage from './pages/Setup'

export default function App() {
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false
  const { pathname } = useLocation()
  const pageName = NAV.find((item) => isActiveNavPath(pathname, item.path))?.label ?? 'Overview'

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
          <PageErrorBoundary key={pathname}>
            <Routes>
              <Route path="/" element={<OverviewPage />} />
              <Route path="/models" element={<ModelsPage />} />
              <Route path="/providers" element={<ProvidersPage />} />
              <Route path="/setup" element={<SetupPage />} />
              <Route path="*" element={<OverviewPage />} />
            </Routes>
          </PageErrorBoundary>
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
        {connected ? 'Live' : 'Offline'}
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
          data-active={isActiveNavPath(pathname, path) || undefined}>
          <Icon size={20} stroke={1.7} aria-hidden />
          <span>{label}</span>
        </UnstyledButton>
      ))}
    </Box>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const data: SegmentedControlItem<MantineColorScheme>[] = [
    { value: 'light', label: <IconSun size={16} aria-label="Light" /> },
    { value: 'auto', label: <IconDeviceDesktop size={16} aria-label="Auto" /> },
    { value: 'dark', label: <IconMoonStars size={16} aria-label="Dark" /> },
  ]
  return <SegmentedControl size="xs" aria-label="Color scheme" data={data} value={colorScheme} onChange={setColorScheme} />
}

export function Fade({ pending, children }: { pending: boolean; children: React.ReactNode }) {
  return <Box style={{ opacity: pending ? 0.65 : 1, transition: 'opacity 200ms' }}>{children}</Box>
}
