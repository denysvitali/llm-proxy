import { useState, type CSSProperties, type ReactNode } from 'react'
import { Accordion, Badge, Box, Group, Text, Title } from '@mantine/core'
import { IconWallet } from '@tabler/icons-react'
import { useMediaQuery } from '@mantine/hooks'

// Subscription usage band. Quota data is operational state, so on a desktop it
// is visible without a click; the header still carries a real expand/collapse
// control so the operator can fold the band away. Phones start collapsed to
// keep the page short., and the same control opens it.
export function OverviewSubscriptions({ accounts, children }: {
  accounts: string[]
  children: ReactNode
}) {
  // Resolve the breakpoint synchronously so the first paint already matches
  // the viewport: desktop starts open, phones start collapsed.
  const compact = useMediaQuery('(max-width: 48em)', false, { getInitialValueInEffect: false })
  const [open, setOpen] = useState(!compact)
  const panelId = 'overview-subscription-panel'
  // Two columns balance around the tallest card (masonry), so no dead band
  // opens under a short card. A single account takes the full width.
  const columns = Math.min(Math.max(accounts.length, 1), 2)
  const panelStyle = { '--usage-cols': columns } as CSSProperties
  const value = open ? 'usage' : null

  return (
    <Box component="section" className="overview-subscriptions" aria-labelledby="overview-subscriptions-title">
      <Accordion
        variant="unstyled"
        value={value}
        // Desktop is pinned open: a click on the control is a no-op there, so
        // quota data can never be hidden (and the control still reads as a
        // normal disclosure to assistive tech and to the screenshot harness).
        onChange={(next) => { if (compact) setOpen(next === 'usage') }}
        chevronPosition="right"
        className="overview-subscriptions-accordion"
      >
        <Accordion.Item value="usage">
          <Accordion.Control aria-controls={panelId} className="overview-subscriptions-control">
            <Group justify="space-between" gap="xs" wrap="nowrap" miw={0} pr="sm">
              <Group gap={8} wrap="nowrap" align="center" miw={0}>
                <span className="overview-feed-icon" aria-hidden="true"><IconWallet size={14} stroke={1.7} /></span>
                <Box miw={0}>
                  <Title order={3} id="overview-subscriptions-title" fz={14} fw={650} lh={1.3}>Subscription usage</Title>
                  <Text size="xs" c="dimmed">{accounts.join(' · ')} account quotas · live</Text>
                </Box>
              </Group>
              <Badge variant="light" color="gray" tt="none" size="sm" style={{ flex: 'none' }}>
                {accounts.length} {accounts.length === 1 ? 'account' : 'accounts'}
              </Badge>
            </Group>
          </Accordion.Control>
          <Accordion.Panel id={panelId}>
            <div className="overview-usage-grid" style={panelStyle}>
              {children}
            </div>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Box>
  )
}
