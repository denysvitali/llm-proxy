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

// EasyUI — the easyui.site monochrome system, mapped onto Mantine. Near-black
// ink on near-white in light, near-white ink on near-black in dark, with the
// single blue (#3B82F6) reserved for the live indicator. Card surfaces stay
// #ffffff (light) and #171a21 (dark): palette.ts charts were validated against
// exactly those two surfaces. Tokens live in index.css.

// Zinc ramp (EasyUI's dark palette) replaces Mantine's neutral dark so panels,
// popovers, and modals share one family with the canvas.
const dark: [string, string, string, string, string, string, string, string, string, string] = [
  '#F2F2F3', // 0 — ink
  '#A1A1A6',
  '#6F6F75',
  '#4D4D52',
  '#343438',
  '#29292C',
  '#202023',
  '#1C1C1F',
  '#18181A',
  '#111113',
]

// Interaction accent: EasyUI's accent is the ink itself, inverted per scheme.
const zinc: [string, string, string, string, string, string, string, string, string, string] = [
  '#FAFAFA',
  '#F4F4F5',
  '#E4E4E7',
  '#A1A1AA',
  '#71717A',
  '#3F3F46',
  '#27272A',
  '#18181B',
  '#0A0A0A',
  '#050505',
]

export const theme = createTheme({
  primaryColor: 'zinc',
  autoContrast: true,
  respectReducedMotion: true,
  colors: { dark, zinc },
  primaryShade: { light: 8, dark: 0 },
  defaultRadius: 'md',
  cursorType: 'pointer',
  fontFamily:
    '"Geist", "Geist Sans", Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
  fontFamilyMonospace: '"JetBrains Mono", "Fira Code", ui-monospace, monospace',
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
