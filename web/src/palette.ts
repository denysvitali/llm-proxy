// Chart palettes validated with the dataviz palette validator
// (contrast/CVD checks) against both surfaces. Do not tweak by eye.

export const categorical = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500'],
} as const

// Ordinal one-hue ramp for ordered percentiles (p50 -> p99, light -> dark).
export const ordinal = {
  light: ['#86b6ef', '#2a78d6', '#104281'],
  dark: ['#6da7ec', '#256abf', '#184f95'],
} as const

// Single hue for magnitude bars (requests by model).
export const magnitude = {
  light: '#2a78d6',
  dark: '#3987e5',
} as const

// Reserved status colors — identical in both modes, always paired with an
// icon + label so state is never color-alone.
export const status = {
  good: '#0ca30c',
  warning: '#fab219',
  serious: '#ec835a',
  critical: '#d03b3b',
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
