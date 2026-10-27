import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useId, type ReactNode } from 'react'

// Labeled sections keep screen-reader navigation aligned with the visual hierarchy.
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
    <Box component="section" aria-labelledby={titleId} miw={0}>
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm" mb="sm">
        <Stack gap={1} style={{ flex: '1 1 16rem', minWidth: 0, overflowWrap: 'anywhere' }}>
          <Title order={3} id={titleId} fz={14} fw={650} m={0} lh={1.3}>
            {title}
          </Title>
          {description && (
            <Text size="xs" c="dimmed" lh={1.5} m={0}>
              {description}
            </Text>
          )}
        </Stack>
        {extra != null && (
          <Group gap="xs" wrap="wrap" miw={0} maw="100%" style={{ flexShrink: 0 }}>
            {extra}
          </Group>
        )}
      </Group>
      {children}
    </Box>
  )
}
