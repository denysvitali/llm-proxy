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
      styles={{
        root: { maxWidth: '100%', flexShrink: 0 },
        label: {
          minHeight: 44,
          minWidth: 44,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          transitionDuration: reduceMotion ? '0ms' : undefined,
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
