import { Stack, Text, ThemeIcon } from '@mantine/core'
import type { ReactNode } from 'react'

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
    <Stack role="status" aria-atomic="true" align="center" py="xl" px="sm" gap={6} miw={0} style={{ overflowWrap: 'anywhere' }}>
      <ThemeIcon variant="light" color="gray" size="lg" radius="xl" aria-hidden="true">
        {icon}
      </ThemeIcon>
      <Text fw={600} ta="center" maw="100%">{title}</Text>
      {hint && (
        <Text size="sm" c="dimmed" ta="center" w="100%" maw={340}>
          {hint}
        </Text>
      )}
    </Stack>
  )
}
