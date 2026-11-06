import { Text } from '@mantine/core'

// A single summary band: one hairline-divided surface, micro-labels over big
// tabular figures. Attention values keep the warning hue (never color alone —
// the label says what the number means).
export function CatalogSummary({
  label,
  items,
}: {
  label: string
  items: { label: string; value: string; hint: string; attention?: boolean }[]
}) {
  return (
    <section className="catalog-summary-band" aria-label={label}>
      {items.map((item) => (
        <div
          key={item.label}
          className="catalog-summary-cell"
          data-attention={item.attention ? 'true' : undefined}
        >
          <Text className="micro-label catalog-summary-label">{item.label}</Text>
          <Text className="catalog-summary-value tabular">{item.value}</Text>
          <Text size="xs" c="dimmed" className="catalog-summary-hint-text">
            {item.hint}
          </Text>
        </div>
      ))}
    </section>
  )
}
