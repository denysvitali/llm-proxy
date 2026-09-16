import { Box, Group, Text } from '@mantine/core'
import { useId, type ReactNode } from 'react'

export function PageSection({
  title,
  description,
  extra,
  children,
}: {
  title: string
  description?: string
  extra?: ReactNode
  children: ReactNode
}) {
  const titleId = useId()

  return (
    <Box component="section" aria-labelledby={titleId} mb="lg" miw={0}>
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm" mb="sm">
        <Box style={{ flex: '1 1 16rem', minWidth: 0, maxWidth: '100%', overflowWrap: 'anywhere' }}>
          <Text component="h2" id={titleId} fz={11} tt="uppercase" c="dimmed" fw={600} m={0} style={{ letterSpacing: '0.06em' }}>
            {title}
          </Text>
          {description && (
            <Text size="sm" c="dimmed" mt={2}>
              {description}
            </Text>
          )}
        </Box>
        {extra != null && (
          <Group gap="xs" wrap="wrap" miw={0} maw="100%">
            {extra}
          </Group>
        )}
      </Group>
      {children}
    </Box>
  )
}
