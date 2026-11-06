import { SegmentedControl, VisuallyHidden } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'

const ranges = [
  { value: '1h', description: 'Last 1 hour' },
  { value: '6h', description: 'Last 6 hours' },
  { value: '24h', description: 'Last 24 hours' },
  { value: '7d', description: 'Last 7 days' },
]

export function TimeRangeControl({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  disabled?: boolean
}) {
  const reduceMotion = useReducedMotion()

  return (
    <SegmentedControl
      size="xs"
      aria-label="Time range"
      value={value}
      onChange={onChange}
      disabled={disabled}
      transitionDuration={reduceMotion ? 0 : 220}
      radius="md"
      styles={{
        root: {
          maxWidth: '100%',
          flexShrink: 0,
          background: 'var(--surface-3, var(--sunken))',
          padding: 3,
          border: '1px solid var(--hairline)',
        },
        indicator: { boxShadow: 'var(--shadow-subtle)', border: '1px solid var(--hairline-subtle, var(--hairline))' },
        label: {
          minHeight: 38,
          minWidth: 44,
          paddingInline: 12,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontWeight: 600,
          fontSize: 12,
          letterSpacing: '-0.01em',
        },
      }}
      data={ranges.map(({ value, description }) => ({
        value,
        label: (
          <>
            <span aria-hidden="true">{value}</span>
            <VisuallyHidden>{description}</VisuallyHidden>
          </>
        ),
      }))}
    />
  )
}
