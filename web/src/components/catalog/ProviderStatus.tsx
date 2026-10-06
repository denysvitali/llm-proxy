import { useId, type ReactNode } from 'react'
import { Button, Group, Stack, Text, Title } from '@mantine/core'
import { IconLogin } from '@tabler/icons-react'
import type { OverviewBackend } from '../../api'
import { ACCOUNT_AUTH } from '../../lib/accounts'

// A native indicator plus explicit text keeps configuration state readable
// without color; theme roles match the rest of the health chrome.
export function StatusDot({ ok, okLabel, badLabel }: { ok: boolean; okLabel: string; badLabel: string }) {
  return (
    <Group gap="xs" wrap="nowrap" miw={0}>
      <span className="catalog-status-dot" aria-hidden="true" data-ok={ok} />
      <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
        {ok ? okLabel : badLabel}
      </Text>
    </Group>
  )
}

// Auth state for one provider: account-backed backends show account sign-in
// state plus a link to the login flow; key-based ones show key presence.
export function AuthStatus({ backend }: { backend: OverviewBackend }) {
  const account = ACCOUNT_AUTH[backend.name]
  if (account) {
    return (
      <Group gap="xs" miw={0} wrap="wrap">
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
          color="brand"
          variant={backend.authConfigured ? 'subtle' : 'light'}
          onClick={(event) => event.stopPropagation()}
          leftSection={<IconLogin size={12} stroke={1.8} aria-hidden="true" />}
        >
          {backend.authConfigured ? 'Sign in again' : 'Sign in'}
        </Button>
      </Group>
    )
  }
  return <StatusDot ok={backend.hasKey} okLabel="API key set" badLabel="API key missing" />
}

export function CardSection({ title, children }: { title: string; children: ReactNode }) {
  const titleId = useId()
  return (
    <Stack component="section" aria-labelledby={titleId} gap="xs" miw={0}>
      <Title id={titleId} order={5} mb={0} style={{ overflowWrap: 'anywhere' }}>
        {title}
      </Title>
      <Group gap={6} miw={0} align="flex-start" wrap="wrap">
        {children}
      </Group>
    </Stack>
  )
}
