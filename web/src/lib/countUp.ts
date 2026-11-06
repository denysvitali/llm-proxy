import type { ReactNode } from 'react'

// StatTile's count-up. Two phases, re-evaluated on every value change:
//
//   static    value is not a finite number, or motion is reduced: show as-is
//   counting  animate from 0 to `target` over COUNT_UP_MS with an ease-out
//             cubic; the last frame (done) shows exactly `target`
export type CountUpPhase = { kind: 'static' } | { kind: 'counting'; target: number }

export const COUNT_UP_MS = 640

export function countUpPhase(value: ReactNode, enabled: boolean): CountUpPhase {
  return typeof value === 'number' && Number.isFinite(value) && enabled ? { kind: 'counting', target: value } : { kind: 'static' }
}

export function countUpFrame(target: number, elapsedMs: number, duration = COUNT_UP_MS): { shown: number; done: boolean } {
  const progress = Math.min(1, elapsedMs / duration)
  // Ease-out cubic: quick to read, settles softly.
  const eased = 1 - Math.pow(1 - progress, 3)
  return { shown: Math.round(target * eased), done: progress >= 1 }
}
