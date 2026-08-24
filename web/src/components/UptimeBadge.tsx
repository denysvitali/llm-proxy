import { Badge } from '@mantine/core'

interface UptimeBadgeProps {
  uptime: number // fraction 0..1
  requests: number
}

function rgba(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

// Availability state as icon-dot + label (never color alone). Thresholds:
// >=99% healthy, >=90% degraded, otherwise unhealthy; no traffic is neutral.
export default function UptimeBadge({ uptime, requests }: UptimeBadgeProps) {
  if (!requests) {
    return (
      <Badge
        color="gray"
        variant="light"
        leftSection={<Dot color="#9aa2ad" />}
        styles={{ root: { flex: 'none' }, label: { overflow: 'visible' } }}
      >
        no traffic
      </Badge>
    )
  }
  const state =
    uptime >= 0.99
      ? { color: '#0ca30c', label: 'healthy' }
      : uptime >= 0.9
        ? { color: '#fab219', label: 'degraded' }
        : { color: '#d03b3b', label: 'unhealthy' }
  return (
    <Badge
      variant="light"
      leftSection={<Dot color={state.color} />}
      styles={{
        root: { color: state.color, backgroundColor: rgba(state.color, 0.12), flex: 'none' },
        label: { overflow: 'visible' },
      }}
    >
      {state.label}
    </Badge>
  )
}

function Dot({ color }: { color: string }) {
  return (
    <span
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
