import { Alert, Box, Card, Group, Loader, Skeleton, Stack, Text, Title } from '@mantine/core'
import { IconWallet } from '@tabler/icons-react'
import type { ReactNode } from 'react'
import './usage-cards.css'

export default function UsageCardShell({ title, subtitle, isFetching, isPending, error, updated, children, hasData = false, ariaLabel }: {
  title: string
  subtitle?: string
  isFetching: boolean
  isPending: boolean
  error?: Error | null
  updated?: string
  hasData?: boolean
  ariaLabel?: string
  children: ReactNode
}) {
  const updatedAt = updated ? new Date(updated) : undefined
  const validUpdatedAt = updatedAt && Number.isFinite(updatedAt.getTime()) ? updatedAt : undefined
  return (
    <Card component="section" aria-label={ariaLabel ?? title} withBorder radius="lg" p="md" className="usage-card">
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs" mb="md">
        <Group wrap="nowrap" gap="sm" align="flex-start" miw={0}>
          <Box className="usage-card-icon" aria-hidden="true"><IconWallet size={18} stroke={1.6} /></Box>
          <Box miw={0}><Title order={5}>{title}</Title>{subtitle && <Text size="xs" c="dimmed" mt={2}>{subtitle}</Text>}</Box>
        </Group>
        {isFetching && !isPending && <Loader size="xs" aria-label={`Refreshing ${title}`} style={{ flexShrink: 0 }} />}
      </Group>
      {error && <Alert color="red" variant="light" title={hasData ? 'Could not refresh usage' : 'Usage unavailable'} mb={hasData ? 'sm' : 0}>
        <Text size="sm">{error.message}</Text>
        {hasData && <Text size="xs" mt={4}>Showing the last available data.</Text>}
      </Alert>}
      {isPending ? <Stack gap="sm" aria-label={`Loading ${title}`}><Skeleton height={32} width="45%" /><Skeleton height={8} /><Skeleton height={16} width="70%" /></Stack> : children}
      {hasData && <Text size="xs" c="dimmed" className="usage-card-updated" mt="md">
        {validUpdatedAt ? <>Updated <time dateTime={validUpdatedAt.toISOString()} title={validUpdatedAt.toLocaleString()}>{validUpdatedAt.toLocaleTimeString('en-US', { hour12: false })}</time></> : 'Update time unavailable'}
      </Text>}
    </Card>
  )
}
