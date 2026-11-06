import { useId, type ReactNode } from 'react'
import { Button, Group, Stack, Text, Title } from '@mantine/core'
import { IconLogin } from '@tabler/icons-react'
import type { OverviewBackend } from '../../api'
import { ACCOUNT_AUTH } from '../../lib/accounts'

// A native indicator plus explicit text keeps configuration state readable
// without color; status tokens match the rest of the health chrome.
export function StatusDot({
  ok,
  okLabel,
  badLabel,
}: {
  ok: boolean
  okLabel: string
  badLabel: string
}) {
  return (
    <Group gap={6} wrap="nowrap" miw={0} className="catalog-status">
      <span className="catalog-status-dot" aria-hidden="true" data-ok={ok} />
      <Text size="xs" c="dimmed" className="catalog-status-label">
        {ok ? okLabel : badLabel}
      </Text>
    </Group>
  )
}

// Auth state for one provider: account-backed backends show account sign-in
// state plus a link to the login flow; key-based ones show key presence.
export function AuthStatus({ backend, compact }: { backend: OverviewBackend; compact?: boolean }) {
  const account = ACCOUNT_AUTH[backend.name]
  if (account) {
    return (
      <Group gap={compact ? 10 : 'xs'} miw={0} wrap="wrap" className="catalog-status">
        <StatusDot
          ok={backend.authConfigured}
          okLabel={`${account.label} account signed in`}
          badLabel={`${account.label} account not signed in`}
        />
        <Button
          component="a"
          href={account.login}
          size="xs"
          mih={44}
          color="gray"
          variant={backend.authConfigured ? 'subtle' : 'light'}
          onClick={(event) => event.stopPropagation()}
          leftSection={<IconLogin size={13} stroke={1.8} aria-hidden="true" />}
        >
          {backend.authConfigured ? 'Sign in again' : 'Sign in'}
        </Button>
      </Group>
    )
  }
  return <StatusDot ok={backend.hasKey} okLabel="API key set" badLabel="API key missing" />
}

// Grouped drawer section: a micro-label heading (still a real h3, so the drawer
// outline is navigable) instead of a titled bordered card.
export function CardSection({
  title,
  hint,
  children,
}: {
  title: string
  hint?: string
  children: ReactNode
}) {
  const titleId = useId()
  return (
    <Stack component="section" aria-labelledby={titleId} gap={6} miw={0} className="catalog-detail-section">
      <Title id={titleId} order={3} className="catalog-detail-label">
        {title}
      </Title>
      {hint && (
        <Text size="xs" c="dimmed" className="catalog-detail-hint">
          {hint}
        </Text>
      )}
      <Group gap={6} miw={0} align="flex-start" wrap="wrap" className="catalog-detail-body">
        {children}
      </Group>
    </Stack>
  )
}
