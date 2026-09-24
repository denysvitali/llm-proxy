import { Badge, Group, Tooltip } from '@mantine/core'

// HTTP status chips for a backend or model.
//
// Severity is the organizing principle, not raw count: a column of chips
// sorted by frequency buries the one 5xx behind a wall of 2xx, which is exactly
// backwards for a console. So codes sort worst-first within the displayed
// limit, while the `+N` overflow keeps the common codes reachable via tooltip.
//
// Every chip is dot + code + count, and each carries an aria-label, so the
// severity is never conveyed by color alone (DESIGN.md §9).
type Severity = 'critical' | 'warning' | 'good' | 'neutral'

const rank: Record<Severity, number> = { critical: 0, warning: 1, good: 2, neutral: 3 }

// 5xx / no response is the operator's problem; 4xx is the client's; 2xx/3xx is
// the healthy baseline; anything else stays neutral rather than guessed at.
function severity(status: string): Severity {
  if (status === 'error' || status.startsWith('5')) return 'critical'
  if (status.startsWith('4')) return 'warning'
  if (status.startsWith('2') || status.startsWith('3')) return 'good'
  return 'neutral'
}

const token: Record<Severity, string> = {
  critical: 'var(--data-critical)',
  warning: 'var(--data-warning)',
  good: 'var(--data-good)',
  neutral: 'var(--mantine-color-dimmed)',
}

function describeStatus(status: string, count: number): string {
  return `${status === 'error' ? 'No HTTP response' : `HTTP ${status}`} · ${count.toLocaleString('en-US')} request${count === 1 ? '' : 's'}`
}

function Dot({ color }: { color: string }) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'inline-block',
        width: 6,
        height: 6,
        borderRadius: '50%',
        backgroundColor: color,
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
    // Worst-first so a 5xx is visible without expanding; count breaks ties
    // within a severity so the most common of two 4xx codes leads.
    .sort((a, b) => {
      const sa = rank[severity(a[0])]
      const sb = rank[severity(b[0])]
      if (sa !== sb) return sa - sb
      return b[1] - a[1] || a[0].localeCompare(b[0])
    })
  if (entries.length === 0) return null

  const visibleLimit = Number.isFinite(limit) ? Math.max(0, Math.floor(limit)) : 4
  const shown = entries.slice(0, visibleLimit)
  const rest = entries.slice(visibleLimit)
  const restLabel = rest.map(([status, count]) => describeStatus(status, count)).join('; ')

  return (
    <Group gap={4} wrap="wrap" justify="flex-end">
      {shown.map(([status, n]) => {
        const sev = severity(status)
        const description = describeStatus(status, n)
        return (
          <Tooltip key={status} label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
            <Badge
              variant="light"
              size="xs"
              tabIndex={0}
              aria-label={description}
              leftSection={<Dot color={token[sev]} />}
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
            variant="light"
            size="xs"
            tabIndex={0}
            aria-label={`${rest.length} more statuses: ${restLabel}`}
            styles={{
              root: { flex: 'none', cursor: 'default', color: 'var(--mantine-color-dimmed)' },
              label: { overflow: 'visible' },
            }}
          >
            +{rest.length}
          </Badge>
        </Tooltip>
      )}
    </Group>
  )
}
