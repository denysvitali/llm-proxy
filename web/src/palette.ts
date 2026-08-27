// Chart palettes validated with the dataviz palette validator
// (lightness band, chroma, CVD ΔE, normal-vision floor, contrast) against the
// Apple design surfaces: #f5f5f7 light and true-black #000000 dark. Do not
// tweak by eye — re-run the validator for the surface you touch.

export const categorical = {
  light: ['#0071e3', '#0f9d68', '#d9480f', '#bf5af2'],
  dark: ['#3b96e8', '#2ba268', '#c67c0a', '#9d5ce0'],
} as const

// Ordinal one-hue ramp for ordered percentiles (p50 -> p99, light -> dark).
// Third step leans indigo on dark: CVD separation within one hue collapses on
// black, so the ramp bends hue slightly while staying monotone in lightness.
export const ordinal = {
  light: ['#6ba9ec', '#3579cf', '#1e5aa8'],
  dark: ['#4a9bf5', '#2f7ad6', '#2062b8'],
} as const

// Single hue for magnitude bars (requests by model).
export const magnitude = {
  light: '#0071e3',
  dark: '#3b96e8',
} as const

// Reserved status colors — iOS system green/amber/orange/red. Always paired
// with an icon + label so state is never color-alone.
export const status = {
  good: '#30d158',
  warning: '#ffd60a',
  serious: '#ff9f0a',
  critical: '#ff453a',
} as const

export const seriesNames = ['input', 'output', 'cache read', 'cache write'] as const

import { useColorScheme } from '@mantine/hooks'

// Resolves the active color scheme ('auto' included) and returns the
// validated chart palette for that mode.
export function useChartPalette() {
  const scheme = useColorScheme('light')
  const dark = scheme === 'dark'
  return {
    dark,
    series: dark ? [...categorical.dark] : [...categorical.light],
    ramp: dark ? [...ordinal.dark] : [...ordinal.light],
    magnitude: dark ? magnitude.dark : magnitude.light,
  }
}
