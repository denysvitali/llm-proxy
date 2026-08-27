import { Group, Text, Tooltip } from '@mantine/core'

// statusChipColor maps an upstream HTTP status (or "error" for transport
// failures) to the palette's reserved status colors.
function chipStyle(status: string): { bg: string; fg: string; label: string } {
  if (status === 'error') {
    return { bg: 'rgba(255, 69, 58, 0.13)', fg: '#ff453a', label: 'no response' }
  }
  if (status.startsWith('4')) {
    return { bg: 'rgba(255, 214, 10, 0.15)', fg: '#ffd60a', label: '' }
  }
  if (status.startsWith('5')) {
    return { bg: 'rgba(255, 69, 58, 0.13)', fg: '#ff453a', label: '' }
  }
  return { bg: 'rgba(255, 214, 10, 0.15)', fg: '#ffd60a', label: '' }
}

// Amber is unreadable as small text on white — text-grade variants keep the
// same hue family in light mode, echoing UptimeBadge's approach.
function chipFg(status: string, dark: boolean): string {
  if (!dark && status !== 'error') {
    return status.startsWith('5') ? '#d70015' : '#b25000'
  }
  return chipStyle(status).fg
}

export default function StatusChips({
  codes,
  limit = 4,
}: {
  codes?: Record<string, number>
  limit?: number
}) {
  const entries = Object.entries(codes ?? {})
    .filter(([, n]) => n > 0)
    .sort((a, b) => b[1] - a[1])
  if (entries.length === 0) return null

  // Dark mode comes from the document class Mantine manages; reading it here
  // avoids threading colorScheme through every table row.
  const dark = document.documentElement.classList.contains('dark')
  const shown = entries.slice(0, limit)
  const rest = entries.slice(limit)

  return (
    <Group gap={4} wrap="nowrap" justify="flex-end">
      {shown.map(([status, n]) => {
        const style = chipStyle(status)
        return (
          <Tooltip key={status} label={`${status === 'error' ? 'No HTTP response' : `HTTP ${status}`} · ${n.toLocaleString('en-US')} request${n === 1 ? '' : 's'}`} withArrow>
            <span
              style={{
                display: 'inline-block',
                padding: '1px 7px',
                borderRadius: 999,
                fontSize: 11,
                fontWeight: 600,
                lineHeight: '16px',
                fontVariantNumeric: 'tabular-nums',
                backgroundColor: style.bg,
                color: chipFg(status, dark),
                cursor: 'default',
              }}
            >
              {status === 'error' ? 'err' : status} · {n}
            </span>
          </Tooltip>
        )
      })}
      {rest.length > 0 && (
        <Tooltip label={rest.map(([s, n]) => `${s}: ${n}`).join(' · ')} withArrow multiline>
          <Text size="xs" c="dimmed" style={{ cursor: 'default' }}>+{rest.length}</Text>
        </Tooltip>
      )}
    </Group>
  )
}
