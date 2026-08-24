import {
  IconGauge,
  IconCpu,
  IconStack2,
  IconSettings,
} from '@tabler/icons-react'
import type { ComponentType } from 'react'

export interface NavItem {
  path: string
  label: string
  icon: ComponentType<{ size?: number | string; stroke?: number }>
}

// Order defines both the desktop header order and the mobile bottom-bar order.
export const NAV: NavItem[] = [
  { path: '/', label: 'Overview', icon: IconGauge },
  { path: '/models', label: 'Models', icon: IconCpu },
  { path: '/providers', label: 'Providers', icon: IconStack2 },
  { path: '/setup', label: 'Setup', icon: IconSettings },
]
