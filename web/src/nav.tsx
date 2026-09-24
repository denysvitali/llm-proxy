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
// This is the correctness guard for the shell's active state: `/models-old`
// must never light up the Models tab, and a deep route like `/models/x`
// must. Keep the segment boundary in the comparison.
export function isActiveNavPath(pathname: string, path: string): boolean {
  return pathname === path || (path !== '/' && pathname.startsWith(`${path}/`))
}

// Order defines both the desktop header order and the mobile bottom-bar order.
// The shell renders this as a segmented in-header console on >= 48em and as a
// bottom tab bar below it; both views read the same array so the two can never
// disagree about which route is active.
export const NAV: NavItem[] = [
  { path: '/', label: 'Overview', icon: IconActivity },
  { path: '/models', label: 'Models', icon: IconCube },
  { path: '/providers', label: 'Providers', icon: IconServer },
  { path: '/setup', label: 'Setup', icon: IconTerminal2 },
]
