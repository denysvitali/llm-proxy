import { Box, Group, Stack, Text, Title } from '@mantine/core'
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
    <Box component="section" aria-labelledby={titleId} mb="xl" miw={0}>
      <Group
        justify="space-between"
        align={description ? 'flex-end' : 'center'}
        wrap="wrap"
        gap="sm"
        mb="md"
      >
        <Stack gap={4} style={{ flex: '1 1 16rem', minWidth: 0, maxWidth: '100%', overflowWrap: 'anywhere' }}>
          <Title
            order={2}
            size="h4"
            id={titleId}
            fw={600}
            m={0}
            lh={1.3}
            style={{ letterSpacing: '-0.015em' }}
          >
            {title}
          </Title>
          {description && (
            <Text size="sm" c="dimmed" lh={1.45} m={0}>
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
