import { Group, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

export function PageHeader({ title, subtitle, extra }: {
  title: string
  subtitle?: ReactNode
  extra?: ReactNode
}) {
  return (
    <header className="page-header">
      <div className="page-header-copy">
        <Title order={1} className="page-title">{title}</Title>
        {subtitle && <Text component="div" className="page-subtitle">{subtitle}</Text>}
      </div>
      {extra != null && <Group className="page-header-actions" gap="xs">{extra}</Group>}
    </header>
  )
}
