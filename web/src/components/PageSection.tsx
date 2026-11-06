import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useId, type ReactNode } from 'react'
import './primitives.css'

// Labeled sections keep screen-reader navigation aligned with the visual
// hierarchy. The heading is the micro-label voice (11px/650/.08em, uppercase
// via CSS) so every section on every page reads as one instrument label.
// `dense` tightens the heading-to-content gap for stacked compact blocks.
export function PageSection({
  title,
  description,
  extra,
  dense = false,
  children,
}: {
  title: string
  description?: string
  extra?: ReactNode
  dense?: boolean
  children: ReactNode
}) {
  const titleId = useId()

  return (
    <Box component="section" aria-labelledby={titleId} miw={0} className="page-section" data-dense={dense || undefined}>
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm" className="page-section-head">
        <Stack gap={2} className="page-section-copy">
          <Title order={3} id={titleId} className="page-section-title">
            {title}
          </Title>
          {description && (
            <Text size="xs" c="dimmed" lh={1.5} m={0} className="page-section-description">
              {description}
            </Text>
          )}
        </Stack>
        {extra != null && (
          <Group gap="xs" wrap="wrap" miw={0} maw="100%" className="page-section-extra">
            {extra}
          </Group>
        )}
      </Group>
      <div className="page-section-body">{children}</div>
    </Box>
  )
}
