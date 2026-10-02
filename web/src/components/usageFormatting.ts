import type { GrokUsage } from '../api'

export function usagePercent(usage?: GrokUsage) {
  return usage?.hasPercent && Number.isFinite(usage.percentUsed) && usage.percentUsed >= 0
    ? usage.percentUsed
    : null
}

export function usageTone(percent: number | null) {
  if (percent === null) return 'blue'
  if (percent >= 90) return 'red'
  if (percent >= 70) return 'yellow'
  return 'teal'
}

export function periodTypeLabel(periodType?: string) {
  if (!periodType) return ''
  const trimmed = periodType.replace(/^USAGE_PERIOD_TYPE_/i, '').replace(/_/g, ' ').toLowerCase()
  return trimmed ? trimmed.replace(/^\w/, (ch) => ch.toUpperCase()) : ''
}

export function formatPeriod(start?: string, end?: string, periodType?: string) {
  const kind = periodTypeLabel(periodType)
  if (!start && !end) return kind || 'Current period'
  const startDate = start ? new Date(start) : undefined
  const endDate = end ? new Date(end) : undefined
  const validStart = startDate && !Number.isNaN(startDate.getTime())
  const validEnd = endDate && !Number.isNaN(endDate.getTime())
  const prefix = kind ? `${kind} · ` : ''
  if (validStart && validEnd) return `${prefix}${startDate!.toLocaleDateString()} – ${endDate!.toLocaleDateString()}`
  if (validEnd) return `${prefix}Resets ${endDate!.toLocaleString()}`
  if (validStart) return `${prefix}Started ${startDate!.toLocaleDateString()}`
  return kind || 'Current period'
}
