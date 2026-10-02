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
  description: string
  icon: ComponentType<{ size?: number | string; stroke?: number }>
}

// Match complete path segments, never lookalike prefixes such as /models-old.
// This is the correctness guard for the shell's active state: `/models-old`
// must never light up the Models tab, and a deep route like `/models/x`
// must. Keep the segment boundary in the comparison.
export function isActiveNavPath(pathname: string, path: string): boolean {
  return pathname === path || (path !== '/' && pathname.startsWith(`${path}/`))
}

// Desktop and mobile navigation share routes, labels and active states.
export const NAV: NavItem[] = [
  { path: '/', label: 'Overview', description: 'Traffic & performance', icon: IconActivity },
  { path: '/models', label: 'Models', description: 'Explore your catalog', icon: IconCube },
  { path: '/providers', label: 'Providers', description: 'Connections & usage', icon: IconServer },
  { path: '/setup', label: 'Setup', description: 'Connect your tools', icon: IconTerminal2 },
]
