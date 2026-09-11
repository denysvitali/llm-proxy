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
    <Stack align="center" py="xl" gap={6}>
      <ThemeIcon variant="light" color="gray" size="lg" radius="xl">
        {icon}
      </ThemeIcon>
      <Text fw={600}>{title}</Text>
      {hint && (
        <Text size="sm" c="dimmed" ta="center" maw={340}>
          {hint}
        </Text>
      )}
    </Stack>
  )
}
