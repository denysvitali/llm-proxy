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
  '#edf1f8', // 0 — primary text on dark
  '#dce3f0', // 1
  '#c0cbdc', // 2 — secondary text
  '#a1adc3', // 3 — tertiary / disabled
  '#74829a', // 4 — muted
  '#354159', // 5 — disabled borders
  '#2b303d', // 6 — raised borders
  '#1c202b', // 7 — raised surface
  '#14161a', // 8 — card / drawer surface (chart contrast reference)
  '#0f1117', // 9 — canvas
]

// Indigo carries interaction; charts use palette.ts.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#f0f1ff',
  '#e1e2ff',
  '#c6c7ff',
  '#aeacff',
  '#9693ff', // 4 — dark-mode accent
  '#7b73ed',
  '#5b51df', // 6 — light-mode accent
  '#4d42c5',
  '#4036a2',
  '#352f80',
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
    fontWeight: '650',
    sizes: {
      h1: { fontSize: rem(34), lineHeight: '1.2', fontWeight: '650' },
      h2: { fontSize: rem(18), lineHeight: '1.3', fontWeight: '650' },
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
