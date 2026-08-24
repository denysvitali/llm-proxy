import { Card, createTheme, Paper, rem, Table } from '@mantine/core'

// Brand ramp (indigo-leaning blue) generated for even perceptual steps; used
// as the primary accent. Chart series colors still come from src/palette.ts
// (contrast/CVD-validated) — the theme only dresses the Mantine chrome.
const brand: [string, string, string, string, string, string, string, string, string, string] = [
  '#eef2ff',
  '#dfe6ff',
  '#bcc9ff',
  '#96a9ff',
  '#748dff',
  '#5f7bfb',
  '#5170f4',
  '#425fdd',
  '#3a54c6',
  '#2f47ad',
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 5 },
  autoContrast: true,
  colors: { brand },
  defaultRadius: 'lg',
  cursorType: 'pointer',
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Inter, system-ui, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: '650',
    sizes: {
      h3: { fontSize: rem(20), lineHeight: '1.3' },
      h4: { fontSize: rem(17), lineHeight: '1.35' },
      h5: { fontSize: rem(14), lineHeight: '1.4' },
    },
  },
  fontSizes: { xs: rem(11.5) },
  components: {
    Card: Card.extend({
      defaultProps: { withBorder: true, radius: 'lg', shadow: 'xs' },
    }),
    Paper: Paper.extend({
      defaultProps: { radius: 'lg' },
    }),
    Table: Table.extend({
      defaultProps: { highlightOnHoverColor: 'var(--mantine-color-default-hover)' },
    }),
  },
})
