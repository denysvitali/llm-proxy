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
  '#f4f6f8', // 0 — primary text on dark
  '#dfe3e8', // 1
  '#b8bfc9', // 2 — secondary text
  '#8b93a1', // 3 — tertiary / disabled
  '#6b7280', // 4 — muted
  '#3a414d', // 5 — disabled borders
  '#272d36', // 6 — raised borders
  '#1a1e25', // 7 — raised surface (cards)
  '#14161a', // 8 — toolbar / drawer surface
  '#0b0d10', // 9 — canvas (body background)
]

// Accent ramp. Blue carries interaction, not data; charts use palette.ts.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#e8f1fb', // 0 tint
  '#cfe3f8',
  '#a8cdf1',
  '#7cb2e8',
  '#4e97dc',
  '#2680d8',
  '#0f6fc4', // 6 — filled buttons on light
  '#0a84d8', // 7
  '#3b9ae8', // 8 — filled buttons on dark
  '#66b0f2', // 9
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 8 },
  autoContrast: true,
  respectReducedMotion: true,
  colors: { brand, dark },
  defaultRadius: 'md',
  cursorType: 'pointer',
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Inter, system-ui, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: '650',
    sizes: {
      h1: { fontSize: rem(22), lineHeight: '1.25', fontWeight: '650' },
      h2: { fontSize: rem(18), lineHeight: '1.3', fontWeight: '650' },
      h3: { fontSize: rem(15), lineHeight: '1.35', fontWeight: '650' },
      h4: { fontSize: rem(13), lineHeight: '1.4', fontWeight: '600' },
      h5: { fontSize: rem(12), lineHeight: '1.45', fontWeight: '600' },
    },
  },
  fontSizes: { xs: rem(11.5), sm: rem(13), md: rem(14), lg: rem(16) },
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
      defaultProps: { withBorder: true, radius: 'lg', padding: 'md' },
    }),
    Paper: Paper.extend({
      defaultProps: { radius: 'md' },
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
      defaultProps: { radius: 'sm' },
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
