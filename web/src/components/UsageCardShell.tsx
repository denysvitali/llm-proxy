import { Alert, Card, Group, Loader, Stack, Text, Title } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import type { ReactNode } from 'react'

export default function UsageCardShell({ title, subtitle, isFetching, isPending, error, updated, children }: {
  title: string
  subtitle?: string
  isFetching: boolean
  isPending: boolean
  error?: Error | null
  updated?: string
  children: ReactNode
}) {
  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" gap="xs" mb="sm">
        <Stack gap={0}>
          <Title order={5}>{title}</Title>
          {subtitle && <Text size="xs" c="dimmed">{subtitle}</Text>}
        </Stack>
        {isFetching && <Loader size="xs" aria-hidden="true" />}
      </Group>
      {error && (
        <Alert color="red" variant="light" title="Couldn't refresh usage data" icon={<IconAlertTriangle size={16} />}>
          <Text size="sm">{error.message}</Text>
          <Text size="xs" c="dimmed" mt={4}>Showing the last available data.</Text>
        </Alert>
      )}
      {isPending ? (
        <Group justify="center" py="lg" role="status"><Loader size="sm" /><Text size="sm" c="dimmed">Loading…</Text></Group>
      ) : children}
      {updated && (
        <Text size="xs" c="dimmed" mt="sm">
          Updated <time dateTime={updated}>{new Date(updated).toLocaleString()}</time>
        </Text>
      )}
    </Card>
  )
}
