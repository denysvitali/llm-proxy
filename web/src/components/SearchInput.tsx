import { ActionIcon, TextInput } from '@mantine/core'
import { IconSearch, IconX } from '@tabler/icons-react'
import { useRef } from 'react'

export default function SearchInput({ value, onChange, placeholder = 'Search…', label = 'Search' }: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label?: string
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  return (
    <TextInput
      ref={inputRef}
      className="search-input"
      type="search"
      leftSection={<IconSearch size={17} aria-hidden />}
      rightSection={value ? (
        <ActionIcon aria-label="Clear search" variant="subtle" color="gray" onClick={() => { onChange(''); inputRef.current?.focus() }} size={36}>
          <IconX size={16} stroke={1.8} aria-hidden />
        </ActionIcon>
      ) : undefined}
      rightSectionWidth={44}
      styles={{ input: { minHeight: 44 } }}
      placeholder={placeholder}
      aria-label={label}
      autoComplete="off"
      spellCheck={false}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  )
}
