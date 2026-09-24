import { Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

// Empty state. An idle proxy is the common case, not an edge case, so this has
// to read as informative rather than broken: a quiet outlined glyph (no filled
// colored disc competing with real data), the fact, and then what to do next.
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
      aria-atomic="true"
      align="center"
      py="xl"
      px="sm"
      gap={6}
      miw={0}
      style={{ overflowWrap: 'anywhere' }}
    >
      <span
        aria-hidden="true"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: 40,
          height: 40,
          borderRadius: 'var(--mantine-radius-md)',
          border: '1px solid var(--hairline)',
          color: 'var(--mantine-color-dimmed)',
          marginBottom: 4,
        }}
      >
        {icon}
      </span>
      <Title order={5} fz={13} ta="center" maw="100%">
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
