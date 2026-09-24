import {
  Anchor,
  AppShell,
  Badge,
  Box,
  Container,
  Group,
  type MantineColorScheme,
  SegmentedControl,
  type SegmentedControlItem,
  Stack,
  Text,
  ThemeIcon,
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

// The single breakpoint for the whole console is 48em (see DESIGN.md §7). Above
// it the nav is a segmented console in the header; below it the same NAV array
// becomes a bottom tab bar, so the two can never disagree about the active route.
const MOBILE_QUERY = '(max-width: 48em)'

// Chrome geometry. 48px is the densest header that still clears the 44px
// coarse-pointer touch floor for the brand link and the scheme toggle; the shell
// is a frame, so every pixel it does not spend on signal is wasted.
const HEADER_HEIGHT = 48
// The bottom bar is 60px so each of the four tabs can fill it end to end: that
// clears the 44px coarse-pointer floor with room to spare, and it lets the
// active tab's accent rule sit flush against the footer's top hairline. The
// safe-area inset is handled by padding-bottom in index.css, not by geometry.
const BOTTOM_BAR_HEIGHT = 60
// Tab hit area. The bar is 60px so the accent rule can sit flush against the
// top hairline; the tab itself fills it, which clears the 44px coarse-pointer
// floor without a separate media query.
const BOTTOM_BAR_ITEM_HEIGHT = 60

export default function App() {
  const isMobile = useMediaQuery(MOBILE_QUERY) ?? false

  return (
    <AppShell
      header={{ height: HEADER_HEIGHT }}
      footer={
        isMobile
          ? { height: `calc(${BOTTOM_BAR_HEIGHT}px + env(safe-area-inset-bottom, 0px))` }
          : { height: 0, collapsed: true }
      }
      // Padding lives on Main, not on the shell: AppShell's own padding would
      // also inset the header and the bottom bar, which must be edge-to-edge.
      padding={0}
    >
      <style>{`
        /* Skip link. Off-screen until focused, then a real control parked in the
           top-left of the header so the keyboard path to the content works
           without a permanent tab stop's worth of chrome. */
        .app-skip-link {
          position: fixed;
          top: 6px;
          left: var(--mantine-spacing-sm);
          z-index: 300;
          padding: 6px var(--mantine-spacing-sm);
          border: 1px solid var(--hairline);
          border-radius: var(--mantine-radius-md);
          background: var(--card);
          color: var(--mantine-color-text);
          transform: translateY(-200%);
        }
        .app-skip-link:focus {
          transform: translateY(0);
        }
      `}</style>
      <Anchor href="#main" className="app-skip-link" fw={600} fz="sm">
        Skip to content
      </Anchor>

      {/* Header: opaque --card with a single hairline bottom border (index.css
          already sets both). No backdrop-filter and no shadow — translucency
          over scrolling rows costs legibility for an effect that fights the
          hairline language. */}
      <AppShell.Header withBorder>
        <Container size="xl" h="100%" px="md">
          <Group h="100%" justify="space-between" wrap="nowrap" gap="sm">
            <HeaderBrand />
            {!isMobile && <DesktopNav />}
            <Group gap={8} wrap="nowrap">
              <LiveStatusBadge />
              <ColorSchemeToggle />
            </Group>
          </Group>
        </Container>
      </AppShell.Header>

      <AppShell.Main id="main" tabIndex={-1}>
        {/* Dense frame: the old shell spent 16px of padding on every edge and 40
            on the bottom before a single row of signal. Full-bleed on mobile
            keeps the page's own hairline grid intact. */}
        <Container
          size="xl"
          px={isMobile ? 0 : 'md'}
          py={isMobile ? 'sm' : 'md'}
          pb={isMobile ? 'md' : 24}
        >
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

  return (
    <Group gap={8} wrap="nowrap" miw={0}>
      <Tooltip label="llm-proxy — overview" openDelay={400}>
        {/* The brand mark stays a link home, but it is a flat, hairline-rimmed
            tile now: no gradient, no inset ring, no drop shadow. A control room
            has no decorative chrome. */}
        <UnstyledButton
          component={NavLink}
          to="/"
          aria-label="llm-proxy home"
          style={{
            display: 'flex',
            borderRadius: 'var(--radius-nav)',
          }}
        >
          <ThemeIcon
            size={24}
            radius="sm"
            variant="filled"
            color="brand"
            style={{ boxShadow: 'inset 0 0 0 1px var(--hairline)' }}
          >
            <span style={{ fontSize: 13, fontWeight: 700, lineHeight: 1 }}>λ</span>
          </ThemeIcon>
        </UnstyledButton>
      </Tooltip>
      {/* The wordmark and the mark are one link, but only the tile is the hit
          target — a 24px box is a far more reliable touch than a text run, and
          hiding the words below 48em keeps the narrow header to three controls. */}
      <Stack gap={0} visibleFrom="xs">
        <Text fz="sm" fw={650} lh={1.1} lts="-0.014em">
          llm-proxy
        </Text>
        <Text fz="xs" c="dimmed" lh={1.1}>
          Gateway
        </Text>
      </Stack>
      {ov?.version && (
        <Badge
          size="xs"
          variant="default"
          fz="xs"
          styles={{ root: { textTransform: 'none' } }}
        >
          <span className="stat-value">v{ov.version}</span>
        </Badge>
      )}
    </Group>
  )
}

// The websocket status is information, not decoration, and its width is
// reserved: at 11.5px "Offline" measures wider than "Live", so an unreserved
// badge would shove the scheme toggle sideways every time the socket connected.
// The fixed w={54} box is the load-bearing part — do not drop it.
function LiveStatusBadge() {
  const connected = useLiveStatsUpdates()

  return (
    <Box visibleFrom="xs" w={54} style={{ display: 'flex', justifyContent: 'center' }}>
      <Tooltip
        label={
          connected
            ? 'Real-time updates connected'
            : 'Real-time updates offline — stats refresh on page load'
        }
        openDelay={300}
      >
        <Badge
          variant="dot"
          color={connected ? 'teal' : 'gray'}
          size="sm"
          // Color is only half the signal; the word is the other half, so state
          // is never carried by the dot alone.
          styles={{ root: { cursor: 'default' }, label: { overflow: 'visible' } }}
          aria-live="polite"
        >
          {connected ? 'Live' : 'Offline'}
        </Badge>
      </Tooltip>
    </Box>
  )
}

function DesktopNav() {
  const { pathname } = useLocation()

  return (
    <Group gap={2} wrap="nowrap">
      {NAV.map((item) => {
        const active = isActiveNavPath(pathname, item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            className="desktop-nav-link"
            data-active={active || undefined}
            px={12}
            h={30}
            // `position: relative` is the parent for the accent rule below.
            style={{
              position: 'relative',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              borderRadius: 'var(--radius-nav)',
              fontSize: 'var(--mantine-font-size-sm)',
              fontWeight: active ? 650 : 500,
              // The active tab is the one interactive accent in the shell.
              color: active ? 'var(--mantine-primary-color-filled)' : 'var(--mantine-color-dimmed)',
              // NavLink's own className merge, plus the hover tint that
              // index.css hangs off `.desktop-nav-link:not([data-active])`.
              background: active ? 'var(--segmented-thumb)' : undefined,
              transition: 'background-color 120ms ease, color 120ms ease',
            }}
          >
            <Icon size={14} stroke={active ? 2 : 1.7} />
            {item.label}
            {active && (
              // A 2px accent bar on the bottom edge — reads at a glance from
              // across a room, and survives a colorblind reading of the state.
              <span
                aria-hidden
                style={{
                  position: 'absolute',
                  left: 8,
                  right: 8,
                  bottom: -1,
                  height: 2,
                  background: 'var(--mantine-primary-color-filled)',
                }}
              />
            )}
          </UnstyledButton>
        )
      })}
    </Group>
  )
}

function BottomNav() {
  const { pathname } = useLocation()

  return (
    <Group
      component="nav"
      aria-label="Main navigation"
      h={BOTTOM_BAR_HEIGHT}
      gap={0}
      px={6}
      grow
      wrap="nowrap"
    >
      {NAV.map((item) => {
        const active = isActiveNavPath(pathname, item.path)
        const Icon = item.icon
        return (
          <UnstyledButton
            key={item.path}
            component={NavLink}
            to={item.path}
            h={BOTTOM_BAR_ITEM_HEIGHT}
            className="bottom-nav-link"
            style={{
              // `position: relative` is the containing block for the accent rule
              // below; without it the bar anchors to the footer and floats.
              position: 'relative',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              gap: 1,
              flex: 1,
              minWidth: 0,
              borderRadius: 'var(--radius-nav)',
              color: active ? 'var(--mantine-primary-color-filled)' : 'var(--mantine-color-dimmed)',
              background: active ? 'var(--segmented-thumb)' : undefined,
            }}
          >
            {active && (
              <span
                aria-hidden
                style={{
                  position: 'absolute',
                  top: 0,
                  left: '22%',
                  right: '22%',
                  height: 2,
                  background: 'var(--mantine-primary-color-filled)',
                }}
              />
            )}
            <Icon size={20} stroke={active ? 2 : 1.6} />
            <Text fz={10} fw={active ? 650 : 500} lh={1}>
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

  // SegmentedControl infers Value = MantineColorScheme from the `value` prop
  // below, so an un-annotated array would widen 'light' | 'auto' | 'dark' to
  // string and fail the item type. The annotation pins the literal union.
  const data: SegmentedControlItem<MantineColorScheme>[] = [
    { value: 'light', label: <IconSun size={14} stroke={1.8} aria-label="Light" /> },
    { value: 'auto', label: <IconDeviceDesktop size={14} stroke={1.8} aria-label="Auto" /> },
    { value: 'dark', label: <IconMoonStars size={14} stroke={1.8} aria-label="Dark" /> },
  ]

  return (
    <Tooltip label="Color scheme" openDelay={400}>
      <SegmentedControl
        size="xs"
        radius="sm"
        aria-label="Color scheme"
        data={data}
        value={colorScheme}
        onChange={setColorScheme}
        // A segmented thumb paints the checked item in near-black/near-white by
        // default, which fights the accent. --sc-label-color re-tints the
        // checked label to the text color and leaves the rest dimmed.
        styles={{ input: { '--sc-label-color': 'var(--mantine-color-text)' } }}
      />
    </Tooltip>
  )
}

// The three raw endpoints are operator plumbing, not navigation. They read as
// dimmed monospace paths in a single quiet line — annotated links would give
// them more visual weight than the actual nav.
function FooterNote() {
  return (
    <Group justify="center" gap={8} mt="md" fz="xs" style={{ color: 'var(--mantine-color-dimmed)' }}>
      <FooterLink href="/stats">/stats</FooterLink>
      <span aria-hidden>/</span>
      <FooterLink href="/api/overview">/api/overview</FooterLink>
      <span aria-hidden>/</span>
      <FooterLink href="/metrics">/metrics</FooterLink>
    </Group>
  )
}

function FooterLink({ href, children }: { href: string; children: string }) {
  return (
    <Anchor
      href={href}
      // Screen readers get the destination, the sighted read gets the path.
      aria-label={`Raw endpoint ${href}`}
      underline="never"
      ff="monospace"
      fz="xs"
      c="dimmed"
      opacity={0.85}
      style={{ transition: 'opacity 120ms ease' }}
    >
      {children}
    </Anchor>
  )
}

// Cross-page contract (DESIGN.md §8): all four pages import this from App.tsx
// and pass the first-load flag, so the name, props, and behavior are fixed —
// it dims to 0.65 only while there is no data yet and stays opaque during
// background refetches.
export function Fade({ pending, children }: { pending: boolean; children: React.ReactNode }) {
  return (
    <Box style={{ opacity: pending ? 0.65 : 1, transition: 'opacity 200ms' }}>{children}</Box>
  )
}
