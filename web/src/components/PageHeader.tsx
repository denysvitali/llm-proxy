import { Group, Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

export function PageHeader({
  title,
  subtitle,
  extra,
}: {
  title: string
  subtitle?: ReactNode
  extra?: ReactNode
}) {
  return (
    <Group className="page-header" justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb="md" miw={0}>
      <Stack gap={4} style={{ flex: '1 1 16rem', minWidth: 0, maxWidth: '100%', overflowWrap: 'anywhere' }}>
        <Title order={1} size="h3" mb={0} style={{ letterSpacing: '-0.03em' }}>
          {title}
        </Title>
        {subtitle && (
          <Text component="div" size="sm" c="dimmed" maw={560}>
            {subtitle}
          </Text>
        )}
      </Stack>
      {extra != null && (
        <Group gap="xs" wrap="wrap" miw={0} maw="100%">
          {extra}
        </Group>
      )}
    </Group>
  )
}
