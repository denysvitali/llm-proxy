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

// Mantine semantic status tones + their icon+label pair. The reserved
// palette.status hues stay reserved for state; the Badge 'light' variant
// derives the tinted background so no hand-mixed rgba is needed.
const states = {
  good: { color: 'teal', Icon: IconCircleCheck, label: 'healthy' },
  warning: { color: 'yellow', Icon: IconAlertTriangle, label: 'degraded' },
  critical: { color: 'red', Icon: IconCircleX, label: 'unhealthy' },
  neutral: { color: 'gray', Icon: IconMinus, label: null },
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
          color={states.neutral.color}
          variant="light"
          tabIndex={0}
          aria-label={description}
          leftSection={<states.neutral.Icon size={13} stroke={2} aria-hidden="true" />}
          styles={{ root: { flex: 'none', cursor: 'default' }, label: { overflow: 'visible' } }}
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
        color={state.color}
        variant="light"
        tabIndex={0}
        aria-label={`${state.label}: ${detail}`}
        leftSection={<state.Icon size={13} stroke={2} aria-hidden="true" />}
        styles={{
          root: { flex: 'none', cursor: 'default', fontWeight: 600, letterSpacing: '-0.01em' }, // hover target, not a click affordance
          label: { overflow: 'visible' },
        }}
      >
        {state.label}
      </Badge>
    </Tooltip>
  )
}
