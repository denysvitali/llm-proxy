import { Box, Group, Text } from '@mantine/core'
import type { ReactNode } from 'react'

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
  return (
    <Box mb="lg">
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm" mb="sm">
        <div>
          <Text fz={11} tt="uppercase" c="dimmed" fw={600} style={{ letterSpacing: '0.06em' }}>
            {title}
          </Text>
          {description && (
            <Text size="sm" c="dimmed" mt={2}>
              {description}
            </Text>
          )}
        </div>
        {extra}
      </Group>
      {children}
    </Box>
  )
}
