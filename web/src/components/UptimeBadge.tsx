import { Badge, Tooltip } from '@mantine/core'
import {
  IconCircleCheck,
  IconAlertTriangle,
  IconCircleX,
  IconMinus,
} from '@tabler/icons-react'

interface UptimeBadgeProps {
  uptime: number // fraction 0..1
  requests: number
}

// Status is carried by icon + label + the reserved status hue — never by
// color alone (DESIGN.md §9). The hue comes from the `--data-*` tokens, which
// are re-stepped per color scheme in index.css; using Mantine's `teal`/`yellow`
// here would bypass the validated ramp and drift on the dark surface.
const states = {
  good: { color: 'var(--data-good)', Icon: IconCircleCheck, label: 'healthy' },
  warning: { color: 'var(--data-warning)', Icon: IconAlertTriangle, label: 'degraded' },
  critical: { color: 'var(--data-critical)', Icon: IconCircleX, label: 'unhealthy' },
  neutral: { color: 'var(--mantine-color-dimmed)', Icon: IconMinus, label: null },
} as const

type StateKey = keyof typeof states

// Availability state as icon + label (never color alone). Thresholds:
// >=99% healthy, >=90% degraded, otherwise unhealthy; no traffic is neutral.
// A tooltip exposes the exact percentage and request count so the coarse
// label doesn't hide the underlying numbers.
export default function UptimeBadge({ uptime, requests }: UptimeBadgeProps) {
  const noTraffic = requests === 0
  const unavailable = !Number.isFinite(requests) || requests < 0
    || !Number.isFinite(uptime) || uptime < 0 || uptime > 1
  if (noTraffic || unavailable) {
    const description = noTraffic ? 'No requests recorded yet' : 'Request success data is unavailable'
    return (
      <Tooltip label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
        <Badge
          variant="light"
          tt="none"
          tabIndex={0}
          aria-label={description}
          leftSection={<states.neutral.Icon size={12} stroke={2} aria-hidden="true" style={{ color: states.neutral.color }} />}
          styles={{
            root: { flex: 'none', cursor: 'default', color: 'var(--mantine-color-dimmed)', background: 'var(--sunken)' },
            label: { overflow: 'visible' },
          }}
        >
          {noTraffic ? 'no traffic' : 'no data'}
        </Badge>
      </Tooltip>
    )
  }
  const key: StateKey = uptime >= 0.99 ? 'good' : uptime >= 0.9 ? 'warning' : 'critical'
  const state = states[key]
  const detail = `${(uptime * 100).toFixed(2)}% of ${requests.toLocaleString('en-US')} requests succeeded`
  return (
    <Tooltip label={detail} withArrow events={{ hover: true, focus: true, touch: true }}>
      <Badge
        variant="light"
        tt="none"
        tabIndex={0}
        aria-label={`${state.label}: ${detail}`}
        leftSection={<state.Icon size={12} stroke={2} aria-hidden="true" style={{ color: state.color }} />}
        styles={{
          root: { flex: 'none', cursor: 'default', fontWeight: 600, letterSpacing: 0, color: state.color, background: `color-mix(in srgb, ${state.color} 12%, var(--card))` },
          label: { overflow: 'visible' },
        }}
      >
        {state.label}
      </Badge>
    </Tooltip>
  )
}
