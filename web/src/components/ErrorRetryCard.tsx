import { Box, Button, Group, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react'
import type { ReactNode } from 'react'
import './primitives.css'

// A calm, bordered inline alert rather than a shouty banner. The icon tile,
// title and body keep the error legible; the retry affordance carries the only
// color, and it stays a real 44px target on coarse pointers.
export default function ErrorRetryCard({ title, message, onRetry, retrying }: {
  title: string
  message: ReactNode
  onRetry: () => unknown
  retrying: boolean
}) {
  return (
    <Box component="section" role="alert" className="error-retry-card">
      <Group gap="sm" align="flex-start" wrap="nowrap">
        <Box className="error-retry-icon" aria-hidden="true">
          <IconAlertTriangle size={16} stroke={1.8} />
        </Box>
        <Stack gap={4} className="error-retry-copy">
          <Text className="error-retry-title">{title}</Text>
          <Text size="sm" c="dimmed" className="error-retry-message">{message}</Text>
        </Stack>
      </Group>
      <Button
        className="error-retry-action press"
        size="xs"
        variant="default"
        loading={retrying}
        disabled={retrying}
        leftSection={!retrying ? <IconRefresh size={14} aria-hidden /> : undefined}
        onClick={() => { if (!retrying) void onRetry() }}
      >
        Retry loading
      </Button>
    </Box>
  )
}
