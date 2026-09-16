import { rem, SegmentedControl, VisuallyHidden } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'

const ranges = [
  { value: '1h', description: 'Last 1 hour' },
  { value: '6h', description: 'Last 6 hours' },
  { value: '24h', description: 'Last 24 hours' },
  { value: '7d', description: 'Last 7 days' },
]

const trackStyle = {
  background: 'var(--segmented-track)',
  padding: 2,
} as const

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
      radius="sm"
      aria-label="Time range"
      value={value}
      onChange={onChange}
      disabled={disabled}
      // iOS segmented track: hairline thumb on the shared --segmented-*
      // materials so the control matches the header pill-nav.
      styles={{
        root: { maxWidth: '100%', flexShrink: 0, ...trackStyle },
        indicator: {
          backgroundColor: 'var(--segmented-thumb)',
          boxShadow: 'var(--segmented-thumb-shadow)',
          transitionDuration: reduceMotion ? '0ms' : undefined,
        },
        label: {
          color: 'var(--mantine-color-text)',
          minHeight: rem(44),
          minWidth: rem(44),
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: `0 ${rem(8)}`,
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
