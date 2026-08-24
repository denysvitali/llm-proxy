import { Box, Text } from '@mantine/core'
import type { ReactNode } from 'react'
import { useChartPalette } from '../palette'
import { fmtSec, fmtTps } from '../format'

interface PercentileBarsProps {
  values: { p50: number; p90: number; p99: number }
  unit: 's' | 'tok/s'
}

const labels = ['p50', 'p90', 'p99']

// Ordered percentiles drawn on an ordinal one-hue ramp (light -> dark =
// low -> high tail). Bars anchor the baseline at the left, round the data
// end; the exact number sits beside every bar so nothing is color-gated.
export default function PercentileBars({ values, unit }: PercentileBarsProps) {
  const pal = useChartPalette()
  const v = [values.p50, values.p90, values.p99]
  const max = Math.max(...v)
  const fmt = unit === 's' ? fmtSec : fmtTps

  return (
    <Box>
      {v.map((val, i) => {
        const w = max > 0 && val > 0 ? Math.max((val / max) * 100, 1.5) : 0
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
                borderRadius: 4,
              }}
            >
              {w > 0 && (
                <div
                  style={{
                    width: `${w}%`,
                    height: '100%',
                    background: pal.ramp[i],
                    borderRadius: '0 4px 4px 0',
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
