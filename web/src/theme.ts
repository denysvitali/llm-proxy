import { Card, createTheme, Paper, rem, Table } from '@mantine/core'

// Apple-inspired design tokens. Two ideas carry the whole system:
//
// 1. True black dark mode. OLED-grade #000 base with surfaces that lighten
//    slightly as content stacks — like iOS tab bars and visionOS glass over
//    black, not a gray slab.
// 2. Light mode is Apple's marketing surface: #F5F5F7 "apple gray" canvas,
//    white cards, SF-type hierarchy, hairline borders.
//
// Chart series colors still come from src/palette.ts (contrast/CVD-validated
// against both surfaces) — the theme dresses the Mantine chrome only.

// Mantine dark ramp tuned toward pitch black: dark-7..9 collapse to near-
// black so cards sit on #000; dark-4/5 stay visible enough for hairlines.
const dark: [string, string, string, string, string, string, string, string, string, string] = [
  '#f5f5f7', // 0 — primary text on dark
  '#e8e8ed', // 1
  '#c7c7cc', // 2 — iOS secondaryLabel
  '#aeaeb2', // 3 — iOS tertiaryLabel
  '#8e8e93', // 4 — iOS systemGray
  '#58585d', // 5 — hairlines, disabled
  '#3a3a3f', // 6 — raised surface borders
  '#26262b', // 7 — raised surface
  '#161619', // 8 — secondary surface (toolbar/drawer)
  '#000000', // 9 — body background, true black
]

// Apple system colors, tuned for AA on both surfaces. Blue is Apple's system
// blue (light #007AFF / dark #0A84FF) — not the old indigo ramp.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#eaf3ff', // 0 light tint
  '#d4e6ff',
  '#a8ccff',
  '#7cb2ff',
  '#4d97ff',
  '#2680ff',
  '#0071e3', // 6 — Apple marketing blue (buttons on light)
  '#007aff', // 7 — iOS system blue light
  '#0a84ff', // 8 — iOS system blue dark
  '#3395ff', // 9
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 8 },
  autoContrast: true,
  colors: { brand, dark },
  defaultRadius: 'lg',
  cursorType: 'pointer',
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", Roboto, "Helvetica Neue", Inter, system-ui, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: '700',
    sizes: {
      h3: { fontSize: rem(21), lineHeight: '1.25', fontWeight: '700' },
      h4: { fontSize: rem(17), lineHeight: '1.3', fontWeight: '700' },
      h5: { fontSize: rem(14), lineHeight: '1.35', fontWeight: '650' },
    },
  },
  fontSizes: { xs: rem(11.5) },
  components: {
    Card: Card.extend({
      defaultProps: { withBorder: true, radius: 'lg' },
    }),
    Paper: Paper.extend({
      defaultProps: { radius: 'lg' },
    }),
    Table: Table.extend({
      defaultProps: { highlightOnHoverColor: 'var(--mantine-color-default-hover)' },
    }),
  },
})