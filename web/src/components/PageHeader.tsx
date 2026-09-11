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
    <Group className="page-header" justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb="md">
      <Stack gap={4}>
        <Title order={3} mb={0} style={{ letterSpacing: '-0.03em' }}>
          {title}
        </Title>
        {subtitle && (
          <Text size="sm" c="dimmed" maw={560}>
            {subtitle}
          </Text>
        )}
      </Stack>
      {extra}
    </Group>
  )
}
