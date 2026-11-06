import { ActionIcon, TextInput } from '@mantine/core'
import { IconSearch, IconX } from '@tabler/icons-react'
import { useRef } from 'react'
import './primitives.css'

// A raised, pill-ish search field: leading glyph, inline clear, accent focus
// ring, and a 44px floor so it stays a comfortable touch target. The label is
// supplied by the caller and passed straight through as the accessible name.
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
      type="search"
      className="search-input"
      leftSection={<IconSearch size={16} aria-hidden className="search-input-glyph" />}
      rightSection={value ? (
        <ActionIcon
          aria-label="Clear search"
          variant="subtle"
          color="gray"
          className="search-input-clear"
          onClick={() => { onChange(''); inputRef.current?.focus() }}
          size={36}
        >
          <IconX size={16} stroke={1.8} aria-hidden />
        </ActionIcon>
      ) : undefined}
      rightSectionWidth={44}
      placeholder={placeholder}
      aria-label={label}
      autoComplete="off"
      spellCheck={false}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  )
}
