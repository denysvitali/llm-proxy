import { Group, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'

const pageIdentity: Record<string, { index: string; label: string; note: string }> = {
  Overview: { index: '01', label: 'MONITOR / GATEWAY OPERATIONS', note: 'One gateway. Every request in view.' },
  Models: { index: '02', label: 'DISCOVER / MODEL LIBRARY', note: 'Find the right model for your next request.' },
  Providers: { index: '03', label: 'MANAGE / UPSTREAM CONNECTIONS', note: 'Your infrastructure, connected.' },
  Setup: { index: '04', label: 'CONNECT / CLIENT WORKBENCH', note: 'From endpoint to first request.' },
}

export function PageHeader({ title, subtitle, extra }: {
  title: string
  subtitle?: ReactNode
  extra?: ReactNode
}) {
  const identity = pageIdentity[title] ?? pageIdentity.Overview
  return (
    <section className="page-hero" data-page={title.toLowerCase()}>
      <div className="hero-copy">
        <Text component="span" className="page-eyebrow">{identity.label}</Text>
        <Title order={1} className="page-title">{title}</Title>
        {subtitle && <Text component="div" className="page-subtitle">{subtitle}</Text>}
        {extra != null && <Group className="page-header-actions" gap="xs" mt="lg">{extra}</Group>}
      </div>
      <div className="hero-diagram" aria-hidden="true">
        <span className="hero-index">{identity.index}</span>
        <div className="hero-network"><i /><i /><i /><b>λ</b></div>
        <span className="hero-note">{identity.note}</span>
      </div>
    </section>
  )
}
