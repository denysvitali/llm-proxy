import { Badge, Group, Tooltip } from '@mantine/core'

// Contract color roles: teal = good (2xx/3xx), yellow = warning (4xx),
// red = error/critical (5xx / no HTTP response), gray = anything unexpected.
// The 1xx/other bucket is rare enough that neutral gray beats guessing.
function chipColor(status: string): 'teal' | 'yellow' | 'red' | 'gray' {
  if (status === 'error' || status.startsWith('5')) return 'red'
  if (status.startsWith('4')) return 'yellow'
  if (status.startsWith('2') || status.startsWith('3')) return 'teal'
  return 'gray'
}

function describeStatus(status: string, count: number): string {
  return `${status === 'error' ? 'No HTTP response' : `HTTP ${status}`} · ${count.toLocaleString('en-US')} request${count === 1 ? '' : 's'}`
}

// Status-class dot so meaning is never color alone: filled dot per chip,
// keyed to the same role the badge text sits in.
function Dot({ color }: { color: string }) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'inline-block',
        width: 7,
        height: 7,
        borderRadius: '50%',
        backgroundColor: `var(--mantine-color-${color}-filled)`,
      }}
    />
  )
}

export default function StatusChips({
  codes,
  limit = 4,
}: {
  codes?: Record<string, number>
  limit?: number
}) {
  const entries = Object.entries(codes ?? {})
    .filter(([, n]) => Number.isFinite(n) && n > 0)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  if (entries.length === 0) return null

  const visibleLimit = Number.isFinite(limit) ? Math.max(0, Math.floor(limit)) : 4
  const shown = entries.slice(0, visibleLimit)
  const rest = entries.slice(visibleLimit)
  const restLabel = rest.map(([status, count]) => describeStatus(status, count)).join('; ')

  return (
    <Group gap={4} wrap="wrap" justify="flex-end">
      {shown.map(([status, n]) => {
        const color = chipColor(status)
        const description = describeStatus(status, n)
        return (
          <Tooltip key={status} label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
            <Badge
              color={color}
              variant="light"
              size="xs"
              tabIndex={0}
              aria-label={description}
              leftSection={<Dot color={color} />}
              styles={{
                root: { flex: 'none', cursor: 'default', fontVariantNumeric: 'tabular-nums' },
                label: { overflow: 'visible' },
              }}
            >
              {status === 'error' ? 'err' : status} · {n.toLocaleString('en-US')}
            </Badge>
          </Tooltip>
        )
      })}
      {rest.length > 0 && (
        <Tooltip label={restLabel} withArrow multiline w={280} events={{ hover: true, focus: true, touch: true }}>
          <Badge
            color="gray"
            variant="light"
            size="xs"
            tabIndex={0}
            aria-label={`${rest.length} more statuses: ${restLabel}`}
            styles={{ root: { flex: 'none', cursor: 'default' }, label: { overflow: 'visible' } }}
          >
            +{rest.length}
          </Badge>
        </Tooltip>
      )}
    </Group>
  )
}
