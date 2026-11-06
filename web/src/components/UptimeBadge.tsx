import { Tooltip } from '@mantine/core'
import {
  IconCircleCheck,
  IconAlertTriangle,
  IconCircleX,
  IconMinus,
} from '@tabler/icons-react'
import { uptimeReading } from '../lib/stats'
import './primitives.css'

interface UptimeBadgeProps {
  uptime: number // fraction 0..1
  requests: number
}

// Status is carried by icon + dot + label + the reserved status hue — never by
// color alone (DESIGN.md). The hue comes from the `--data-*` tokens, which are
// re-stepped per color scheme in index.css; using Mantine's `teal`/`yellow`
// here would bypass the validated ramp and drift on the dark surface.
const states = {
  good: { color: 'var(--data-good)', Icon: IconCircleCheck, label: 'healthy' },
  warning: { color: 'var(--data-warning)', Icon: IconAlertTriangle, label: 'degraded' },
  critical: { color: 'var(--data-critical)', Icon: IconCircleX, label: 'unhealthy' },
  neutral: { color: 'var(--muted)', Icon: IconMinus, label: null },
} as const

type StateKey = keyof typeof states

// Availability state as icon + label (never color alone). Thresholds:
// >=99% healthy, >=90% degraded, otherwise unhealthy; no traffic is neutral.
// A tooltip exposes the exact percentage and request count so the coarse
// label doesn't hide the underlying numbers.
export default function UptimeBadge({ uptime, requests }: UptimeBadgeProps) {
  const reading = uptimeReading(uptime, requests)
  if (reading.kind !== 'measured') {
    const noTraffic = reading.kind === 'no-traffic'
    const description = noTraffic ? 'No requests recorded yet' : 'Request success data is unavailable'
    return (
      <Tooltip label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
        <span className="status-pill" data-tone="neutral" tabIndex={0} aria-label={description}>
          <states.neutral.Icon size={12} stroke={2} aria-hidden="true" style={{ color: states.neutral.color }} />
          <span className="status-pill-label">{noTraffic ? 'no traffic' : 'no data'}</span>
        </span>
      </Tooltip>
    )
  }
  const key: StateKey = reading.tone
  const state = states[key]
  const detail = `${(reading.uptime * 100).toFixed(2)}% of ${reading.requests.toLocaleString('en-US')} requests succeeded`
  return (
    <Tooltip label={detail} withArrow events={{ hover: true, focus: true, touch: true }}>
      <span className="status-pill" data-tone={key} tabIndex={0} aria-label={`${state.label}: ${detail}`}>
        <state.Icon size={12} stroke={2} aria-hidden="true" style={{ color: state.color }} />
        <span className="status-pill-label">{state.label}</span>
      </span>
    </Tooltip>
  )
}
