import { SegmentedControl } from '@mantine/core'

export function TimeRangeControl({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  disabled?: boolean
}) {
  return (
    <SegmentedControl
      size="xs"
      radius="sm"
      aria-label="Time range"
      value={value}
      onChange={onChange}
      disabled={disabled}
      data={[
        { value: '1h', label: '1h' },
        { value: '6h', label: '6h' },
        { value: '24h', label: '24h' },
        { value: '7d', label: '7d' },
      ]}
    />
  )
}
