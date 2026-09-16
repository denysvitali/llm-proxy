import { Group, Text, Tooltip, useComputedColorScheme } from '@mantine/core'

// Keep the reserved status palette; use its text-grade variants on light.
function chipStyle(status: string, dark: boolean): { bg: string; fg: string } {
  const critical = status === 'error' || status.startsWith('5')
  return critical
    ? { bg: 'rgba(255, 69, 58, 0.13)', fg: dark ? '#ff453a' : '#d70015' }
    : { bg: 'rgba(255, 214, 10, 0.15)', fg: dark ? '#ffd60a' : '#b25000' }
}

function describeStatus(status: string, count: number): string {
  return `${status === 'error' ? 'No HTTP response' : `HTTP ${status}`} · ${count.toLocaleString('en-US')} request${count === 1 ? '' : 's'}`
}

export default function StatusChips({
  codes,
  limit = 4,
}: {
  codes?: Record<string, number>
  limit?: number
}) {
  const dark = useComputedColorScheme('light') === 'dark'
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
        const style = chipStyle(status, dark)
        const description = describeStatus(status, n)
        return (
          <Tooltip key={status} label={description} withArrow events={{ hover: true, focus: true, touch: true }}>
            <span
              tabIndex={0}
              aria-label={description}
              style={{
                display: 'inline-block',
                padding: '1px 7px',
                borderRadius: 999,
                fontSize: 11,
                fontWeight: 600,
                lineHeight: '16px',
                fontVariantNumeric: 'tabular-nums',
                whiteSpace: 'nowrap',
                backgroundColor: style.bg,
                color: style.fg,
                cursor: 'default',
              }}
            >
              {status === 'error' ? 'err' : status} · {n.toLocaleString('en-US')}
            </span>
          </Tooltip>
        )
      })}
      {rest.length > 0 && (
        <Tooltip label={restLabel} withArrow multiline w={280} events={{ hover: true, focus: true, touch: true }}>
          <Text
            component="span"
            tabIndex={0}
            aria-label={`${rest.length} more statuses: ${restLabel}`}
            size="xs"
            c="dimmed"
            style={{ cursor: 'default', whiteSpace: 'nowrap' }}
          >
            +{rest.length}
          </Text>
        </Tooltip>
      )}
    </Group>
  )
}
