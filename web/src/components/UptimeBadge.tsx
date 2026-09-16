import { Badge, Tooltip, useComputedColorScheme } from '@mantine/core'

interface UptimeBadgeProps {
  uptime: number // fraction 0..1
  requests: number
}

function rgba(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

// iOS system colors per mode: text-grade variants on light (systemGreen's
// #34c759 fails as text on white), vivid variants on black.
const statusColors = {
  light: { good: '#248a3d', warning: '#b25000', critical: '#d70015', neutral: '#8e8e93' },
  dark: { good: '#30d158', warning: '#ff9f0a', critical: '#ff453a', neutral: '#8e8e93' },
} as const

// Availability state as icon-dot + label (never color alone). Thresholds:
// >=99% healthy, >=90% degraded, otherwise unhealthy; no traffic is neutral.
// A tooltip exposes the exact percentage and request count so the coarse
// label doesn't hide the underlying numbers.
export default function UptimeBadge({ uptime, requests }: UptimeBadgeProps) {
  const colorScheme = useComputedColorScheme('light')
  const colors = colorScheme === 'dark' ? statusColors.dark : statusColors.light

  const noTraffic = requests === 0
  const unavailable = !Number.isFinite(requests) || requests < 0
    || !Number.isFinite(uptime) || uptime < 0 || uptime > 1
  if (noTraffic || unavailable) {
    const description = noTraffic ? 'No requests recorded yet' : 'Request success data is unavailable'
    return (
      <Tooltip label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
        <Badge
          color="gray"
          variant="light"
          tabIndex={0}
          aria-label={description}
          leftSection={<Dot color={colors.neutral} />}
          styles={{ root: { flex: 'none' }, label: { overflow: 'visible' } }}
        >
          {noTraffic ? 'no traffic' : 'no data'}
        </Badge>
      </Tooltip>
    )
  }
  const state =
    uptime >= 0.99
      ? { color: colors.good, label: 'healthy' }
      : uptime >= 0.9
        ? { color: colors.warning, label: 'degraded' }
        : { color: colors.critical, label: 'unhealthy' }
  return (
    <Tooltip
      label={`${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`}
      withArrow
      events={{ hover: true, focus: true, touch: true }}
    >
      <Badge
        variant="light"
        tabIndex={0}
        aria-label={`${state.label}: ${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`}
        leftSection={<Dot color={state.color} />}
        styles={{
          root: {
            color: state.color,
            backgroundColor: rgba(state.color, 0.13),
            flex: 'none',
            cursor: 'default', // hover target, not a click affordance
            fontWeight: 600,
            letterSpacing: '-0.01em',
          },
          label: { overflow: 'visible' },
        }}
      >
        {state.label}
      </Badge>
    </Tooltip>
  )
}

function Dot({ color }: { color: string }) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'inline-block',
        width: 8,
        height: 8,
        borderRadius: '50%',
        backgroundColor: color,
      }}
    />
  )
}
