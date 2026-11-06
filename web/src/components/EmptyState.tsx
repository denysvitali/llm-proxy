import { Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'
import './primitives.css'

// Empty states explain the next step while keeping the surrounding layout
// stable. `action` is optional so every existing caller keeps compiling; the
// icon tile is decorative and aria-hidden, so the accessible signal stays the
// role="status" text.
export function EmptyState({
  icon,
  title,
  hint,
  action,
}: {
  icon: ReactNode
  title: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <Stack
      role="status"
      aria-atomic="true"
      align="center"
      className="empty-state"
      gap={0}
    >
      <span aria-hidden="true" className="empty-state-mark">
        {icon}
      </span>
      <Title order={5} className="empty-state-title">
        {title}
      </Title>
      {hint && (
        <Text size="xs" c="dimmed" className="empty-state-hint">
          {hint}
        </Text>
      )}
      {action != null && <div className="empty-state-action">{action}</div>}
    </Stack>
  )
}
