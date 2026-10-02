import { Box, Paper, Text } from '@mantine/core'

export function CatalogSummary({ label, items }: {
  label: string
  items: { label: string; value: string; hint: string; attention?: boolean }[]
}) {
  return (
    <Paper component="section" aria-label={label} withBorder radius="md" className="catalog-summary">
      {items.map((item) => (
        <Box key={item.label} className="catalog-summary-item" data-attention={item.attention}>
          <Text size="xs" c="dimmed">{item.label}</Text>
          <Text className="catalog-summary-number tabular">{item.value}</Text>
          <Text size="xs" c="dimmed" className="catalog-summary-hint">{item.hint}</Text>
        </Box>
      ))}
    </Paper>
  )
}
