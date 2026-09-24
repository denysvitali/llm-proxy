import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useId, type ReactNode } from 'react'

// A titled band of the page. The heading is small-caps and quiet so the
// content inside it stays the loudest thing in the section; the `extra` slot
// (a TimeRangeControl, a refresh action) right-aligns on the same baseline.
// Sections are `<section>` + `aria-labelledby` so a screen reader can jump
// between them by landmark.
export function PageSection({
  title,
  description,
  extra,
  children,
}: {
  title: string
  description?: string
  extra?: ReactNode
  children: ReactNode
}) {
  const titleId = useId()

  return (
    <Box component="section" aria-labelledby={titleId} mb="xl" miw={0}>
      <Group
        justify="space-between"
        align="flex-end"
        wrap="wrap"
        gap="sm"
        mb="sm"
        style={{ borderBottom: '1px solid var(--hairline)', paddingBottom: 8 }}
      >
        <Stack gap={2} style={{ flex: '1 1 16rem', minWidth: 0, maxWidth: '100%', overflowWrap: 'anywhere' }}>
          <Title
            order={2}
            id={titleId}
            fz={11}
            tt="uppercase"
            c="dimmed"
            fw={700}
            m={0}
            lh={1.3}
            style={{ letterSpacing: '0.07em' }}
          >
            {title}
          </Title>
          {description && (
            <Text size="xs" c="dimmed" lh={1.4} m={0}>
              {description}
            </Text>
          )}
        </Stack>
        {extra != null && (
          <Group gap="xs" wrap="wrap" miw={0} maw="100%" style={{ flexShrink: 0 }}>
            {extra}
          </Group>
        )}
      </Group>
      {children}
    </Box>
  )
}
