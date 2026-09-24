// Chart palettes for the llm-proxy control-room UI.
//
// DO NOT tweak these by eye. Every value below was derived with the dataviz
// skill's validator, which enforces six machine-checked properties:
// OKLCH lightness band, chroma floor, adjacent-pair CVD separation
// (min of protanopia/deuteranopia simulation), a normal-vision ΔE floor, and
// WCAG contrast against the actual card surface each mode renders on.
//
//   node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode light --surface #ffffff
//   node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode dark  --surface #14161a
//   node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode light --surface #ffffff --ordinal
//
// Current results (all PASS, no relief/warn bands):
//   categorical light  worst adjacent CVD ΔE 19.3 (deutan), normal-vision 20.6
//   categorical dark   worst adjacent CVD ΔE 11.8 (deutan), normal-vision 21.2
//   ordinal    light   monotone, gaps >= 0.06, light-end 2.20:1
//   ordinal    dark    monotone, gaps >= 0.06, light-end 2.15:1
//
// The two modes are separately stepped, not an automatic flip: dark series sit
// in a narrower, lighter-lower L band (0.48-0.67) because CVD separation
// collapses within a single hue against near-black.

import { useComputedColorScheme } from '@mantine/core'

// Fixed categorical order. Slot 0 is always `input`, slot 1 `output`, and so
// on down `seriesNames` — a series keeps its hue when a filter removes others,
// because color follows the entity and never its rank. A 5th series is never
// a generated hue: it folds into "Other" or becomes small multiples.
export const categorical = {
  light: ['#036eae', '#48a260', '#8a3400', '#8b54a2'],
  dark: ['#0083cf', '#3aa85b', '#af4803', '#8c4ca6'],
} as const

// One-hue ramp for ordered data (percentile p50 -> p99). Ordered categories
// read as a ramp, so they take a single hue rather than categorical hues; the
// categorical checks FAIL a correct ramp by design.
export const ordinal = {
  light: ['#8cb2dd', '#4383c8', '#00539a'],
  dark: ['#1d4f82', '#2773c0', '#649cda'],
} as const

// Single hue for magnitude bars (requests by model) — one measure, one color.
export const magnitude = {
  light: '#036eae',
  dark: '#0083cf',
} as const

// Reserved status colors. Never reuse these as a series color, and always ship
// them with an icon or a text label so state is never carried by color alone.
export const status = {
  good: '#1a9e5f',
  warning: '#c98a00',
  serious: '#e07b1a',
  critical: '#d63b30',
} as const

export const seriesNames = ['input', 'output', 'cache read', 'cache write'] as const

// Resolved UI surfaces, exposed so chart axes/grid can dress themselves to the
// same card the chart actually sits on instead of guessing.
export const surfaces = {
  light: { canvas: '#f4f5f7', card: '#ffffff', hairline: 'rgba(0, 0, 0, 0.10)' },
  dark: { canvas: '#0b0d10', card: '#14161a', hairline: 'rgba(255, 255, 255, 0.10)' },
} as const

// Resolves the active color scheme ('auto' included) and returns the validated
// palette for that mode.
export function useChartPalette() {
  const scheme = useComputedColorScheme('light')
  const dark = scheme === 'dark'
  return {
    dark,
    series: dark ? [...categorical.dark] : [...categorical.light],
    ramp: dark ? [...ordinal.dark] : [...ordinal.light],
    magnitude: dark ? magnitude.dark : magnitude.light,
    surface: dark ? surfaces.dark.card : surfaces.light.card,
  }
}
