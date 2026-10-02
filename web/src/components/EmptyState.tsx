import { Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

// Empty states explain the next step while keeping the surrounding layout stable.
export function EmptyState({
  icon,
  title,
  hint,
}: {
  icon: ReactNode
  title: string
  hint?: string
}) {
  return (
    <Stack
      role="status"
      className="empty-state"
      aria-atomic="true"
      align="center"
      py="lg"
      px="sm"
      gap={6}
      miw={0}
      style={{ overflowWrap: 'anywhere' }}
    >
      <span
        aria-hidden="true"
        className="empty-state-icon"
      >
        {icon}
      </span>
      <Title order={5} fz={14} ta="center" maw="100%">
        {title}
      </Title>
      {hint && (
        <Text size="xs" c="dimmed" ta="center" lh={1.45} w="100%" maw={380}>
          {hint}
        </Text>
      )}
    </Stack>
  )
}
