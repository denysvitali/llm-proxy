import { Box, Text } from '@mantine/core'
import type { ReactNode } from 'react'
import { useChartPalette } from '../palette'
import { fmtSec, fmtTps } from '../format'

interface PercentileBarsProps {
  values: { p50: number; p90: number; p99: number }
  unit: 's' | 'tok/s'
  /** Shared upper bound so sibling charts (TTFT vs E2E) read on one scale. */
  max?: number
}

const labels = ['p50', 'p90', 'p99']

// Ordered percentiles drawn on an ordinal one-hue ramp (light -> dark =
// low -> high tail). Bars anchor the baseline at the left and round only the
// data end (pill cap); exact numbers sit in an aligned right-hand column so
// values stay scannable and nothing is encoded by color alone.
export default function PercentileBars({ values, unit, max }: PercentileBarsProps) {
  const pal = useChartPalette()
  const v = [values.p50, values.p90, values.p99]
  // Without an explicit max each chart normalizes to its own tallest bar;
  // with one, all three bars share the caller's scale for cross-chart
  // comparison (e.g. TTFT next to E2E in the model drawer).
  const scaleMax = max && max > 0 ? max : Math.max(...v)
  const fmt = unit === 's' ? fmtSec : fmtTps

  return (
    <Box>
      {v.map((val, i) => {
        const w =
          scaleMax > 0 && val > 0 ? Math.max((val / scaleMax) * 100, 1.5) : 0
        return (
          <Box
            key={labels[i]}
            style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '3px 0' }}
            title={`${labels[i]}: ${fmt(val)}${unit === 's' ? '' : ' tok/s'}`}
          >
            <Text w={30} size="sm" c="dimmed">
              {labels[i]}
            </Text>
            <Box
              style={{
                flex: 1,
                height: 14,
                background: pal.dark
                  ? 'rgba(255,255,255,0.06)'
                  : 'rgba(0,0,0,0.05)',
                borderRadius: 999,
              }}
            >
              {w > 0 && (
                <div
                  style={{
                    width: `${w}%`,
                    height: '100%',
                    background: pal.ramp[i],
                    borderRadius: '0 999px 999px 0',
                    transition: 'width 250ms ease',
                  }}
                />
              )}
            </Box>
            <NumText>{fmt(val)}</NumText>
          </Box>
        )
      })}
    </Box>
  )
}

function NumText({ children }: { children: ReactNode }) {
  return (
    <Text
      w={64}
      size="sm"
      ta="right"
      style={{ fontVariantNumeric: 'tabular-nums' }}
    >
      {children}
    </Text>
  )
}
