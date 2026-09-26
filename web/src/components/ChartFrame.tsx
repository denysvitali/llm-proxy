import { Box, Group, Text, Title } from '@mantine/core'
import { useId } from 'react'
import type { ReactNode } from 'react'

export default function ChartFrame({ title, description, hasData, children, dataTable }: {
  title: string
  description?: string
  hasData: boolean
  children: ReactNode
  dataTable?: ReactNode
}) {
  const titleId = useId()
  return (
    <Box miw={0}>
      <Group justify="space-between" align="baseline" gap="xs" mb="xs">
        <Title order={5} id={titleId} style={{ overflowWrap: 'anywhere' }}>{title}</Title>
        {description && <Text size="xs" c="dimmed">{description}</Text>}
      </Group>
      {hasData ? children : (
        <Text size="sm" c="dimmed" ta="center" py="lg" role="status">No recorded samples in this range</Text>
      )}
      {dataTable}
    </Box>
  )
}
