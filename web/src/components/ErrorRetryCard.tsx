import { Alert, Button, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react'
import type { ReactNode } from 'react'

export default function ErrorRetryCard({ title, message, onRetry, retrying }: {
  title: string
  message: ReactNode
  onRetry: () => unknown
  retrying: boolean
}) {
  return (
    <Alert color="red" variant="light" title={title} icon={<IconAlertTriangle size={16} />}>
      <Stack gap="sm" align="flex-start">
        <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{message}</Text>
        <Button size="xs" variant="light" color="red" loading={retrying} disabled={retrying}
          leftSection={<IconRefresh size={14} />} onClick={() => { if (!retrying) void onRetry() }}>
          Retry loading
        </Button>
      </Stack>
    </Alert>
  )
}
