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

// Waypoint — warm-paper light, harbor-dark, teal navigation accent.
// Chart series colors live in palette.ts; status tokens live in index.css.

const dark: [string, string, string, string, string, string, string, string, string, string] = [
  '#ecf1ec', // 0 — ink
  '#cfd8d0', // 1
  '#9aa79c', // 2 — muted
  '#6f7d73', // 3
  '#3a453c', // 4 — border
  '#1f2822', // 5
  '#151c17', // 6 — card
  '#1b241d', // 7 — header / rails
  '#101613', // 8 — canvas
  '#0b100d', // 9
]

// Waypoint accent (teal) is interaction only — links, focus, active nav,
// primary buttons. Chart series stay in palette.ts.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#eef8f5',
  '#d8efe9',
  '#b0e0d6',
  '#83cdc0',
  '#4ecdb9', // 4 — dark accent
  '#26a896',
  '#0f766e', // 6 — light accent
  '#0c5f59',
  '#094c47',
  '#073b37',
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 4 },
  autoContrast: true,
  respectReducedMotion: true,
  colors: { brand, dark },
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
      h2: { fontSize: rem(15), lineHeight: '1.3', fontWeight: '650' },
      h3: { fontSize: rem(14), lineHeight: '1.35', fontWeight: '650' },
      h4: { fontSize: rem(13), lineHeight: '1.4', fontWeight: '650' },
      h5: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '650' },
      h6: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '650' },
    },
  },
  fontSizes: { xs: rem(12), sm: rem(13), md: rem(14), lg: rem(16) },
  spacing: { xs: rem(6), sm: rem(10), md: rem(16), lg: rem(24), xl: rem(32) },
  radius: {
    xs: rem(2),
    sm: rem(4),
    md: rem(6),
    lg: rem(10),
    xl: rem(14),
  },
  components: {
    Card: Card.extend({
      defaultProps: { withBorder: true, radius: 'lg', padding: 'lg' },
    }),
    Paper: Paper.extend({
      defaultProps: { radius: 'lg' },
    }),
    Table: Table.extend({
      defaultProps: { highlightOnHoverColor: 'var(--mantine-color-default-hover)' },
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
