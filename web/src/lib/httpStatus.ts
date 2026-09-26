import type { MantineColor } from '@mantine/core'

export type Severity = 'good' | 'warning' | 'critical' | 'neutral'

export function statusSeverity(status: string): Severity {
  if (status === 'error') return 'critical'
  if (status.startsWith('5')) return 'critical'
  if (status.startsWith('4')) return 'warning'
  if (status.startsWith('2') || status.startsWith('3')) return 'good'
  return 'neutral'
}

export function severityColor(severity: Severity): MantineColor {
  switch (severity) {
    case 'critical': return 'red'
    case 'warning': return 'yellow'
    case 'good': return 'teal'
    default: return 'gray'
  }
}

export function statusDescription(status: string): string {
  if (status === 'error') return 'No HTTP response from upstream'
  return `HTTP ${status}`
}
