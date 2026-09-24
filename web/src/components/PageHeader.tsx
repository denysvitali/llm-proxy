import { Group, Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

// Page title block. Sits above the page's first section and sets the type
// hierarchy for everything under it — a page title is never competing with a
// section title, so `extra` (usually a range control or an action) is aligned
// to the baseline of the title block rather than floated to the top.
export function PageHeader({
  title,
  subtitle,
  extra,
}: {
  title: string
  subtitle?: ReactNode
  extra?: ReactNode
}) {
  return (
    <Group
      className="page-header"
      justify="space-between"
      align="flex-end"
      wrap="wrap"
      gap="sm"
      mb="lg"
      miw={0}
    >
      <Stack gap={3} style={{ flex: '1 1 16rem', minWidth: 0, maxWidth: '100%', overflowWrap: 'anywhere' }}>
        <Title order={1} size="h2" mb={0} lh={1.2}>
          {title}
        </Title>
        {subtitle && (
          <Text component="div" size="sm" c="dimmed" lh={1.45} maw={620}>
            {subtitle}
          </Text>
        )}
      </Stack>
      {extra != null && (
        <Group gap="xs" wrap="wrap" align="center" miw={0} maw="100%" style={{ flexShrink: 0 }}>
          {extra}
        </Group>
      )}
    </Group>
  )
}
