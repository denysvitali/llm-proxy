import { Alert, Box, Card, Group, Loader, Skeleton, Stack, Text, Title } from '@mantine/core'
import { IconWallet } from '@tabler/icons-react'
import type { ReactNode } from 'react'
import './usage-cards.css'

// The shared anatomy of a subscription-usage card:
//   header  — provider mark, title/subtitle, refresh state, updated-at
//   body    — the provider's own hero metric + meters + rows (children)
//   footer  — nothing is appended here; providers own their rows so the card
//             never opens with dead filler space.
// Data handling (query state, the `updated` timestamp, the error copy) is
// unchanged from the pre-restyle version; only the chrome is new.
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
    <Card component="section" aria-label={ariaLabel ?? title} withBorder radius="lg" p={0} className="usage-card">
      <header className="usage-card-head">
        <Group wrap="nowrap" gap="sm" align="center" miw={0} className="usage-card-identity">
          <Box className="usage-card-icon" aria-hidden="true"><IconWallet size={17} stroke={1.6} /></Box>
          <Box miw={0} className="usage-card-titles">
            <Title order={5} className="usage-card-title">{title}</Title>
            {subtitle && <Text size="xs" c="dimmed" className="usage-card-subtitle">{subtitle}</Text>}
          </Box>
        </Group>
        <Group gap="xs" wrap="nowrap" className="usage-card-meta">
          {isFetching && !isPending && <Loader size={14} aria-label={`Refreshing ${title}`} style={{ flexShrink: 0 }} />}
          {hasData && (
            <Text size="xs" c="dimmed" className="usage-card-updated">
              {validUpdatedAt
                ? <>Updated <time dateTime={validUpdatedAt.toISOString()} title={validUpdatedAt.toLocaleString()}>{validUpdatedAt.toLocaleTimeString('en-US', { hour12: false })}</time></>
                : 'Update time unavailable'}
            </Text>
          )}
        </Group>
      </header>
      {error && (
        <Alert color="red" variant="light" className="usage-card-error" title={hasData ? 'Could not refresh usage' : 'Usage unavailable'}>
          <Text size="sm">{error.message}</Text>
          {hasData && <Text size="xs" mt={4}>Showing the last available data.</Text>}
        </Alert>
      )}
      <div className="usage-card-body">
        {isPending ? (
          <Stack gap="sm" aria-label={`Loading ${title}`} className="usage-card-skeleton">
            <Skeleton height={30} width="45%" radius="sm" />
            <Skeleton height={8} radius="xl" />
            <Skeleton height={15} width="70%" radius="sm" />
          </Stack>
        ) : children}
      </div>
    </Card>
  )
}
