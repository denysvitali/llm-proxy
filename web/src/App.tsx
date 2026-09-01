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
      footer={isMobile ? { height: 64 } : { height: 0, collapsed: true }}
      padding="md"
    >
      <AppShell.Header withBorder={false}>
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
        <AppShell.Footer withBorder={false}>
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
        {/* App-icon style mark: SF-rounded square with the λ, like an iOS
            home-screen icon at small size. */}
        <Box
          style={{
            width: 28,
            height: 28,
            borderRadius: 8,
            background: 'linear-gradient(160deg, #2e94ff 0%, #0a6cf0 100%)',
            display: 'grid',
            placeItems: 'center',
            flexShrink: 0,
            boxShadow: 'inset 0 0 0 0.5px rgba(255,255,255,0.25), 0 1px 3px rgba(10, 108, 240, 0.35)',
          }}
        >
          <Text fw={700} c="white" fz={15} lh={1} style={{ letterSpacing: '-0.02em' }}>
            λ
          </Text>
        </Box>
        <Title
          order={4}
          mb={0}
          style={{
            letterSpacing: '-0.022em',
            color: 'var(--mantine-color-text)',
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
          <Badge
            variant="light"
            color={connected ? 'teal' : 'orange'}
            size="sm"
            leftSection={
              <span
                style={{
                  width: 6,
                  height: 6,
                  borderRadius: '50%',
                  background: connected ? '#30d158' : '#ff9f0a',
                  display: 'inline-block',
                }}
              />
            }
            styles={{ root: { cursor: 'default' }, label: { overflow: 'visible' } }}
            aria-label="Live update status"
          >
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
    <Group
      gap={2}
      wrap="nowrap"
      style={{
        background: 'var(--segmented-track)',
        borderRadius: 10,
        padding: 3,
      }}
    >
      {NAV.map((item) => {
        const active = item.path === '/' ? pathname === '/' : pathname.startsWith(item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            px={13}
            py={5}
            style={{
              borderRadius: 8,
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              fontWeight: active ? 600 : 500,
              fontSize: 'var(--mantine-font-size-sm)',
              letterSpacing: '-0.01em',
              color: active ? 'var(--mantine-color-text)' : 'var(--mantine-color-dimmed)',
              background: active ? 'var(--segmented-thumb)' : 'transparent',
              boxShadow: active ? 'var(--segmented-thumb-shadow)' : 'none',
              transition: 'background 160ms ease, color 160ms ease',
            }}
          >
            <Icon size={15} stroke={active ? 2 : 1.7} />
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
              color: active
                ? 'var(--mantine-color-brand-8)'
                : 'var(--mantine-color-dimmed)',
            }}
          >
            <Icon size={22} stroke={active ? 2 : 1.6} />
            <Text fz={10} fw={active ? 650 : 500} lh={1} style={{ letterSpacing: '-0.01em' }}>
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

// Dims the first render only, while there is no data to show yet. Background
// refetches (live stats updates) keep the previous data fully opaque — dimming
// those made the page pulse on a busy proxy.
export function Fade({ pending, children }: { pending: boolean; children: React.ReactNode }) {
  return (
    <Box style={{ opacity: pending ? 0.65 : 1, transition: 'opacity 200ms' }}>{children}</Box>
  )
}
