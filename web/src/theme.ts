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

// Shared typography and controls; chart colors live in palette.ts.

const dark: [string, string, string, string, string, string, string, string, string, string] = [
  '#f3f5f8', // 0 — ink
  '#d7dee8', // 1
  '#a3adbd', // 2 — muted
  '#7d8898', // 3
  '#2c3240', // 4 — border
  '#242a36', // 5
  '#171a21', // 6 — card
  '#10131a', // 7 — sidebar / body
  '#0c0e13', // 8 — canvas
  '#090b10', // 9
]

// Signal accent is interaction only. Chart series stay in palette.ts.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#eef3ff',
  '#dce6ff',
  '#c5d4ff',
  '#a9c0ff',
  '#8eabff', // 4 — dark accent
  '#5b82f0',
  '#2457d6', // 6 — light accent
  '#1c46b0',
  '#173890',
  '#122c70',
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
    'Inter, "Inter var", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", system-ui, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: '600',
    sizes: {
      h1: { fontSize: rem(22), lineHeight: '1.2', fontWeight: '600' },
      h2: { fontSize: rem(14), lineHeight: '1.3', fontWeight: '600' },
      h3: { fontSize: rem(15), lineHeight: '1.35', fontWeight: '650' },
      h4: { fontSize: rem(13), lineHeight: '1.4', fontWeight: '600' },
      h5: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '600' },
    },
  },
  fontSizes: { xs: rem(12), sm: rem(13), md: rem(14), lg: rem(16) },
  spacing: { xs: rem(6), sm: rem(10), md: rem(16), lg: rem(24), xl: rem(32) },
  radius: {
    xs: rem(2),
    sm: rem(4),
    md: rem(8),
    lg: rem(12),
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
      defaultProps: { withArrow: true, openDelay: 250, radius: 'md' },
    }),
    Button: Button.extend({
      defaultProps: { radius: 'md' },
    }),
    Badge: Badge.extend({
      defaultProps: { radius: 'xl' },
    }),
    Chip: Chip.extend({
      defaultProps: { radius: 'md' },
    }),
    ActionIcon: ActionIcon.extend({
      defaultProps: { radius: 'md' },
    }),
    ScrollArea: ScrollArea.extend({
      defaultProps: { type: 'hover' },
    }),
  },
})
