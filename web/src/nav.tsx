import {
  IconActivity,
  IconCube,
  IconServer,
  IconTerminal2,
} from '@tabler/icons-react'
import type { ComponentType } from 'react'

export interface NavItem {
  path: string
  label: string
  icon: ComponentType<{ size?: number | string; stroke?: number }>
}

// Match complete path segments, never lookalike prefixes such as /models-old.
export function isActiveNavPath(pathname: string, path: string): boolean {
  return pathname === path || (path !== '/' && pathname.startsWith(`${path}/`))
}

// Order defines both the desktop header order and the mobile bottom-bar order.
export const NAV: NavItem[] = [
  { path: '/', label: 'Overview', icon: IconActivity },
  { path: '/models', label: 'Models', icon: IconCube },
  { path: '/providers', label: 'Providers', icon: IconServer },
  { path: '/setup', label: 'Setup', icon: IconTerminal2 },
]
