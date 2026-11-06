// Global keyboard shortcuts as an explicit two-state chord machine.
//
//   any       blocked key         → idle                 (nothing)
//   any       ⌘K / Ctrl+K         → idle                 open palette (preventDefault)
//   any       g (no ⌘/Ctrl)       → awaiting-target(now)
//   any       ? (no ⌘/Ctrl)       → idle                 open shortcuts (preventDefault)
//   awaiting  o/m/p/s within 1s   → idle                 navigate
//   any       other key           → idle                 (nothing)
//
// A key is "blocked" when it was already handled, is part of an IME
// composition, auto-repeats, uses Alt, an overlay is open, or the focus is in
// an editable control or dialog. Blocked keys still cancel a pending chord.
export type ChordState = { kind: 'idle' } | { kind: 'awaiting-target'; since: number }

export interface ChordKey {
  key: string
  metaKey: boolean
  ctrlKey: boolean
  blocked: boolean
  now: number
}

export type ChordAction =
  | { type: 'none' }
  | { type: 'open-palette' }
  | { type: 'open-shortcuts' }
  | { type: 'navigate'; path: string }

export interface ChordStep {
  state: ChordState
  action: ChordAction
  preventDefault: boolean
}

export const CHORD_WINDOW_MS = 1000
export const IDLE_CHORD: ChordState = { kind: 'idle' }

const CHORD_TARGETS: Record<string, string> = { o: '/', m: '/models', p: '/providers', s: '/setup' }

const idle = (action: ChordAction = { type: 'none' }, preventDefault = false): ChordStep => ({ state: IDLE_CHORD, action, preventDefault })

export function chordStep(state: ChordState, input: ChordKey): ChordStep {
  if (input.blocked) return idle()
  const modified = input.metaKey || input.ctrlKey
  if (input.key.toLowerCase() === 'k' && modified) return idle({ type: 'open-palette' }, true)
  if (input.key === 'g' && !modified) return { state: { kind: 'awaiting-target', since: input.now }, action: { type: 'none' }, preventDefault: false }
  if (input.key === '?' && !modified) return idle({ type: 'open-shortcuts' }, true)
  const inChord = state.kind === 'awaiting-target' && input.now - state.since < CHORD_WINDOW_MS && !modified
  const path = inChord ? CHORD_TARGETS[input.key] : undefined
  return idle(path ? { type: 'navigate', path } : { type: 'none' })
}

// At most one shell overlay is open at a time.
export type ShellOverlay = 'none' | 'palette' | 'shortcuts'
