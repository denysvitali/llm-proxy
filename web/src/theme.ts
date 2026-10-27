import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Chip,
  createTheme,
  Paper,
  ScrollArea,
  Table,
  Tooltip,
  rem,
} from '@mantine/core'

// "Console" — a Mantine-native dashboard theme. Cool graphite dark mode,
// indigo interaction accent, 8px control radii, 12px cards. Card surfaces
// stay #ffffff (light) and #171a21 (dark): palette.ts charts were validated
// against exactly those two surfaces. Tokens live in index.css.

// Cool graphite ramp replaces Mantine's neutral dark so panels, popovers,
// and modals share one blue-slate family with the canvas.
const dark: [string, string, string, string, string, string, string, string, string, string] = [
  '#e9ecef', // 0 — ink
  '#d5dae2',
  '#a9b1bf',
  '#7e8797',
  '#4c5464',
  '#343b48',
  '#262c38',
  '#1c212b',
  '#14181f',
  '#0d1016',
]

export const theme = createTheme({
  primaryColor: 'indigo',
  autoContrast: true,
  respectReducedMotion: true,
  colors: { dark },
  defaultRadius: 'md',
  cursorType: 'pointer',
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", system-ui, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: '650',
    sizes: {
      h1: { fontSize: rem(24), lineHeight: '1.15', fontWeight: '700' },
      h2: { fontSize: rem(16), lineHeight: '1.3', fontWeight: '650' },
      h3: { fontSize: rem(14), lineHeight: '1.35', fontWeight: '650' },
      h4: { fontSize: rem(13), lineHeight: '1.4', fontWeight: '650' },
      h5: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '650' },
      h6: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '650' },
    },
  },
  fontSizes: { xs: rem(12), sm: rem(13), md: rem(14), lg: rem(16) },
  spacing: { xs: rem(6), sm: rem(10), md: rem(16), lg: rem(24), xl: rem(32) },
  radius: {
    xs: rem(3),
    sm: rem(5),
    md: rem(8),
    lg: rem(12),
    xl: rem(16),
  },
  components: {
    Card: Card.extend({
      defaultProps: { withBorder: true, radius: 'lg', padding: 'lg' },
    }),
    Paper: Paper.extend({
      defaultProps: { radius: 'lg' },
    }),
    Table: Table.extend({
      defaultProps: { verticalSpacing: 'sm', highlightOnHoverColor: 'var(--mantine-color-default-hover)' },
    }),
    Tooltip: Tooltip.extend({
      defaultProps: { withArrow: true, openDelay: 250, radius: 'sm' },
    }),
    Button: Button.extend({
      defaultProps: { radius: 'md' },
    }),
    Badge: Badge.extend({
      defaultProps: { radius: 'sm', fw: 600 },
    }),
    Chip: Chip.extend({
      defaultProps: { radius: 'sm' },
    }),
    ActionIcon: ActionIcon.extend({
      defaultProps: { radius: 'md' },
    }),
    ScrollArea: ScrollArea.extend({
      defaultProps: { type: 'hover' },
    }),
  },
})
