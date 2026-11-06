// Command palette query + highlight. The cursor indexes the filtered matches.
//
//   reset          → query '', cursor 0           (palette opened)
//   query-changed  → query = value, cursor 0       (narrowing resets highlight)
//   move-down      → cursor = min(cursor+1, count-1)
//   move-up        → cursor = max(cursor-1, 0)
//   hover(index)   → cursor = index
//
// With no matches, move-down leaves the cursor at -1 (nothing highlighted)
// until a move-up or a new query brings it back to 0.
export interface PaletteState {
  query: string
  cursor: number
}

export type PaletteEvent =
  | { type: 'reset' }
  | { type: 'query-changed'; query: string }
  | { type: 'move-down'; count: number }
  | { type: 'move-up' }
  | { type: 'hover'; index: number }

export const INITIAL_PALETTE: PaletteState = { query: '', cursor: 0 }

export function paletteReducer(state: PaletteState, event: PaletteEvent): PaletteState {
  switch (event.type) {
    case 'reset': return INITIAL_PALETTE
    case 'query-changed': return { query: event.query, cursor: 0 }
    case 'move-down': return { ...state, cursor: Math.min(state.cursor + 1, event.count - 1) }
    case 'move-up': return { ...state, cursor: Math.max(state.cursor - 1, 0) }
    case 'hover': return { ...state, cursor: event.index }
  }
}
