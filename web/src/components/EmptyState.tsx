import { Stack, Text, ThemeIcon, Title } from '@mantine/core'
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
      <ThemeIcon variant="light" color="gray" size={44} radius="xl" mb="xs" aria-hidden="true">
        {icon}
      </ThemeIcon>
      {/* h5 semantics, md visuals: the hint (size sm) must stay one step down. */}
      <Title order={5} size="md" ta="center" maw="100%">
        {title}
      </Title>
      {hint && (
        <Text size="sm" c="dimmed" ta="center" lh={1.45} w="100%" maw={360}>
          {hint}
        </Text>
      )}
    </Stack>
  )
}
