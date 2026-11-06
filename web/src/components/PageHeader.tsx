import { Group, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'
import './primitives.css'

// The page masthead. `eyebrow` is an optional micro-label above the title — a
// quiet way to say what axis this page belongs to without adding a second
// heading to the document outline.
export function PageHeader({ title, subtitle, extra, eyebrow }: {
  title: string
  subtitle?: ReactNode
  extra?: ReactNode
  eyebrow?: string
}) {
  return (
    <header className="page-header" data-eyebrow={eyebrow ? 'true' : undefined}>
      <div className="page-header-copy">
        {eyebrow && <span className="page-eyebrow">{eyebrow}</span>}
        <Title order={1} className="page-title">{title}</Title>
        {subtitle && <Text component="div" className="page-subtitle">{subtitle}</Text>}
      </div>
      {extra != null && <Group className="page-header-actions" gap="xs">{extra}</Group>}
    </header>
  )
}
