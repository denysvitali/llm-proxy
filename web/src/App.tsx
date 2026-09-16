import {
  Anchor,
  AppShell,
  Badge,
  Box,
  Container,
  Group,
  SegmentedControl,
  Stack,
  Text,
  ThemeIcon,
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
import { NAV, isActiveNavPath } from './nav'
import OverviewPage from './pages/Overview'
import ModelsPage from './pages/Models'
import ProvidersPage from './pages/Providers'
import SetupPage from './pages/Setup'

export default function App() {
  const isMobile = useMediaQuery('(max-width: 48em)') ?? false

  return (
    <AppShell
      header={{ height: 60 }}
      footer={isMobile ? { height: 'calc(64px + env(safe-area-inset-bottom, 0px))' } : { height: 0, collapsed: true }}
      padding="md"
    >
      <style>{`
        .app-skip-link { position: fixed; top: 8px; left: 16px; z-index: 300; padding: 10px 16px; border-radius: 8px; background: var(--mantine-color-body); color: var(--mantine-color-text); transform: translateY(-150%); }
        .app-skip-link:focus { transform: translateY(0); }
      `}</style>
      <Anchor href="#main" className="app-skip-link" fw={600}>
        Skip to content
      </Anchor>
      <AppShell.Header withBorder={false}>
        <Container size="xl" h="100%" px="md">
          <Group h="100%" justify="space-between" wrap="nowrap" gap="sm">
            <HeaderBrand />
            {!isMobile && <DesktopNav />}
            <ColorSchemeToggle />
          </Group>
        </Container>
      </AppShell.Header>

      <AppShell.Main id="main" tabIndex={-1}>
        <Container size="xl" pb={40} px={isMobile ? 0 : 'md'}>
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
    <Group gap={8} wrap="nowrap">
      {/* App-icon style mark: SF-rounded square with the λ, like an iOS
          home-screen icon at small size. Gradient is theme-driven so it tracks
          the brand ramp instead of hard-coded hexes. */}
      <UnstyledButton
        component={NavLink}
        to="/"
        aria-label="llm-proxy — go to Overview"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          flexShrink: 0,
          borderRadius: 10,
          padding: 2,
        }}
      >
        <ThemeIcon
          variant="gradient"
          gradient={{ from: 'brand.8', to: 'brand.6', deg: 160 }}
          size={28}
          radius={8}
          aria-hidden="true"
          style={{
            flexShrink: 0,
            boxShadow:
              'inset 0 0 0 0.5px rgba(255,255,255,0.25), 0 1px 3px color-mix(in srgb, var(--mantine-color-brand-8) 35%, transparent)',
          }}
        >
          <Text fw={700} c="white" fz={15} lh={1} style={{ letterSpacing: '-0.02em' }}>
            λ
          </Text>
        </ThemeIcon>
        <Stack gap={0} visibleFrom="md">
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
          <Text fz={11} c="dimmed" lh={1.2} style={{ letterSpacing: '0.01em' }}>
            Gateway
          </Text>
        </Stack>
        <Title
          order={4}
          mb={0}
          hiddenFrom="md"
          style={{
            letterSpacing: '-0.022em',
            color: 'var(--mantine-color-text)',
          }}
        >
          llm-proxy
        </Title>
      </UnstyledButton>
      {ov?.version && (
        <Badge variant="light" color="gray" size="sm" visibleFrom="xs">
          v{ov.version}
        </Badge>
      )}
      {/*
        Reserve the badge slot so nav and toggle do not shift sideways when
        the connection state flips. */}
      <Box visibleFrom="xs" w={54} style={{ display: 'flex', justifyContent: 'center' }}>
        <Tooltip label={connected ? 'Real-time updates connected' : 'Real-time updates offline — stats refresh on page load'}>
          {/* variant="dot" is Mantine's native status pill: the dot color is a
              theme token (teal = good, gray = neutral) so no hex literals and
              the label carries the state in text, not color alone. */}
          <Badge
            variant="dot"
            color={connected ? 'teal' : 'gray'}
            size="sm"
            styles={{ root: { cursor: 'default' }, label: { overflow: 'visible' } }}
            aria-live="polite"
          >
            {connected ? 'Live' : 'Offline'}
          </Badge>
        </Tooltip>
      </Box>
    </Group>
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
        const active = isActiveNavPath(pathname, item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            px={14}
            py={6}
            className="desktop-nav-link"
            data-active={active || undefined}
            style={(theme) => ({
              borderRadius: 8,
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              fontWeight: active ? 600 : 500,
              fontSize: theme.fontSizes.sm,
              letterSpacing: '-0.01em',
              color: active ? 'var(--mantine-color-text)' : 'var(--mantine-color-dimmed)',
              background: active ? 'var(--segmented-thumb)' : undefined,
              boxShadow: active ? 'var(--segmented-thumb-shadow)' : 'none',
              transition: 'background 160ms ease, color 160ms ease',
            })}
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
    <Group component="nav" aria-label="Main navigation" h={64} gap={0} px={8} grow wrap="nowrap">
      {NAV.map((item) => {
        const active = isActiveNavPath(pathname, item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            style={{
              height: 52,
              borderRadius: 10,
              background: active ? 'var(--segmented-track)' : 'transparent',
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
    <Group justify="center" gap={6} mt="xl" wrap="nowrap">
      <FooterLink href="/stats">/stats</FooterLink>
      <Text fz="xs" c="dimmed" aria-hidden="true">
        ·
      </Text>
      <FooterLink href="/api/overview">/api/overview</FooterLink>
      <Text fz="xs" c="dimmed" aria-hidden="true">
        ·
      </Text>
      <FooterLink href="/metrics">/metrics</FooterLink>
    </Group>
  )
}

// Footer links: muted until hover, with the global focus ring — hairline
// discipline, no underlines on a bare path-list.
function FooterLink({ href, children }: { href: string; children: string }) {
  return (
    <Anchor
      href={href}
      fz="xs"
      c="dimmed"
      underline="never"
      opacity={0.85}
      style={{ transition: 'opacity 160ms ease, color 160ms ease' }}
    >
      {children}
    </Anchor>
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
