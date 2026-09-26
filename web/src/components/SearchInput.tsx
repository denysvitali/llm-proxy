import { ActionIcon, TextInput } from '@mantine/core'
import { IconSearch, IconX } from '@tabler/icons-react'

export default function SearchInput({ value, onChange, placeholder = 'Search…', label = 'Search' }: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label?: string
}) {
  return (
    <TextInput
      leftSection={<IconSearch size={14} />}
      rightSection={value ? (
        <ActionIcon aria-label="Clear search" variant="subtle" color="gray" onClick={() => onChange('')} size="sm">
          <IconX size={14} stroke={1.8} />
        </ActionIcon>
      ) : undefined}
      rightSectionWidth={44}
      styles={{ input: { minHeight: 44 } }}
      placeholder={placeholder}
      aria-label={label}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  )
}
