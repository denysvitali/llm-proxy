import {
  AppShell,
  Badge,
  Box,
  Container,
  Group,
  SegmentedControl,
  Text,
  Title,
  Tooltip,
  UnstyledButton,
  useMantineColorScheme,
} from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconDeviceDesktop, IconMoonStars, IconSun } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { NavLink, Route, Routes, useLocation } from 'react-router-dom'
import { fetchOverview } from './api'
import { useLiveStatsUpdates } from './useLiveUpdates'
import { NAV } from './nav'
import OverviewPage from './pages/Overview'
import ModelsPage from './pages/Models'
import ProvidersPage from './pages/Providers'
import SetupPage from './pages/Setup'

export default function App() {
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  return (
    <AppShell
      header={{ height: 56 }}
      footer={isMobile ? { height: 60 } : { height: 0, collapsed: true }}
      padding="md"
    >
      <AppShell.Header withBorder>
        <Container size="lg" h="100%" px="md">
          <Group h="100%" justify="space-between" wrap="nowrap" gap="sm">
            <HeaderBrand />
            {!isMobile && <DesktopNav />}
            <ColorSchemeToggle />
          </Group>
        </Container>
      </AppShell.Header>

      <AppShell.Main>
        <Container size="lg" pb={isMobile ? 24 : 40} px="md">
          <Routes>
            <Route path="/" element={<OverviewPage />} />
            <Route path="/models" element={<ModelsPage />} />
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/setup" element={<SetupPage />} />
            <Route path="*" element={<OverviewPage />} />
          </Routes>
          <FooterNote />
        </Container>
      </AppShell.Main>

      {isMobile && (
        <AppShell.Footer withBorder>
          <BottomNav />
        </AppShell.Footer>
      )}
    </AppShell>
  )
}

function HeaderBrand() {
  const { data: ov } = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const connected = useLiveStatsUpdates()
  return (
    <NavLink to="/" style={{ textDecoration: 'none', color: 'inherit' }}>
      <Group gap={8} wrap="nowrap">
        <Box
          style={{
            width: 26,
            height: 26,
            borderRadius: 7,
            background: 'linear-gradient(135deg, var(--mantine-color-blue-5), var(--mantine-color-blue-7))',
            display: 'grid',
            placeItems: 'center',
            flexShrink: 0,
          }}
        >
          <Text fw={800} c="white" fz={14} lh={1}>
            λ
          </Text>
        </Box>
      <Title
        order={4}
        mb={0}
        style={{
          letterSpacing: '-0.01em',
          background:
            'linear-gradient(90deg, var(--mantine-color-text), var(--mantine-color-brand-6))',
          WebkitBackgroundClip: 'text',
          WebkitTextFillColor: 'transparent',
        }}
      >
        llm-proxy
      </Title>
        {ov?.version && (
          <Badge variant="light" color="gray" size="sm" visibleFrom="xs">
            v{ov.version}
          </Badge>
        )}
        <Tooltip label={connected ? 'Real-time updates connected' : 'Reconnecting to real-time updates'}>
          <Badge variant="light" color={connected ? 'teal' : 'orange'} size="sm" aria-label="Live update status">
            {connected ? 'Live' : 'Offline'}
          </Badge>
        </Tooltip>
      </Group>
    </NavLink>
  )
}

function DesktopNav() {
  const { pathname } = useLocation()
  return (
    <Group gap={4} wrap="nowrap">
      {NAV.map((item) => {
        const active = item.path === '/' ? pathname === '/' : pathname.startsWith(item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            px="sm"
            py={6}
            style={{
              borderRadius: 'var(--mantine-radius-md)',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              fontWeight: 600,
              fontSize: 'var(--mantine-font-size-sm)',
              color: active ? 'var(--mantine-color-blue-filled)' : 'var(--mantine-color-dimmed)',
              background: active ? 'var(--mantine-color-blue-light)' : 'transparent',
            }}
          >
            <Icon size={16} stroke={1.8} />
            {item.label}
          </UnstyledButton>
        )
      })}
    </Group>
  )
}

function BottomNav() {
  const { pathname } = useLocation()
  return (
    <Group h="100%" gap={0} grow wrap="nowrap">
      {NAV.map((item) => {
        const active = item.path === '/' ? pathname === '/' : pathname.startsWith(item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            style={{
              height: '100%',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              gap: 3,
              color: active ? 'var(--mantine-color-blue-filled)' : 'var(--mantine-color-dimmed)',
            }}
          >
            <Icon size={22} stroke={active ? 2 : 1.6} />
            <Text fz={10} fw={active ? 700 : 500} lh={1}>
              {item.label}
            </Text>
          </UnstyledButton>
        )
      })}
    </Group>
  )
}

function ColorSchemeToggle() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()

  return (
    <Tooltip label="Choose light, system, or dark appearance">
      <SegmentedControl
        size="xs"
        radius="sm"
        aria-label="Color scheme"
        value={colorScheme}
        onChange={(value) => setColorScheme(value as 'light' | 'auto' | 'dark')}
        data={[
          {
            value: 'light',
            label: <IconSun size={14} stroke={1.8} aria-label="Light mode" />,
          },
          {
            value: 'auto',
            label: (
              <IconDeviceDesktop size={14} stroke={1.8} aria-label="System appearance" />
            ),
          },
          {
            value: 'dark',
            label: <IconMoonStars size={14} stroke={1.8} aria-label="Dark mode" />,
          },
        ]}
      />
    </Tooltip>
  )
}

function FooterNote() {
  return (
    <Text ta="center" size="xs" c="dimmed" mt="xl">
      Real-time updates over WebSocket &middot; JSON APIs:{' '}
      <a href="/stats">/stats</a> &middot; <a href="/api/overview">/api/overview</a> &middot;{' '}
      <a href="/metrics">/metrics</a>
    </Text>
  )
}

// Holds the previous render at reduced opacity while a refetch is in flight,
// instead of flashing a loading state over live data.
export function Fade({ fetching, children }: { fetching: boolean; children: React.ReactNode }) {
  return (
    <Box style={{ opacity: fetching ? 0.65 : 1, transition: 'opacity 200ms' }}>{children}</Box>
  )
}
