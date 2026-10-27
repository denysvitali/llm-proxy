import { Box, Card, SimpleGrid, Text } from '@mantine/core'

export function CatalogSummary({ label, items }: {
  label: string
  items: { label: string; value: string; hint: string; attention?: boolean }[]
}) {
  return (
    <Card component="section" p="md" aria-label={label} radius="lg">
      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="md" verticalSpacing="md">
        {items.map((item) => (
          <Box key={item.label} miw={0} data-attention={item.attention}>
            <Text size="xs" c="dimmed">{item.label}</Text>
            <Text fz={22} fw={700} lh={1.2} mt={2} style={{ fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.02em', color: item.attention ? 'var(--data-warning)' : undefined }}>
              {item.value}
            </Text>
            <Text size="xs" c="dimmed">{item.hint}</Text>
          </Box>
        ))}
      </SimpleGrid>
    </Card>
  )
}
