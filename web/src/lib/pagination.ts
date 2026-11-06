// A page index is stored as requested and clamped on read, so a list that
// shrinks under the current page (filter, provider change, viewport switching
// the page size) falls back to its last page without an extra state update.
export interface PageWindow {
  currentPage: number
  lastPage: number
  // 1-based first and last item positions shown on the current page.
  start: number
  end: number
}

export function pageWindow(total: number, pageSize: number, requestedPage: number): PageWindow {
  const lastPage = Math.max(0, Math.ceil(total / pageSize) - 1)
  const currentPage = Math.min(requestedPage, lastPage)
  return {
    currentPage,
    lastPage,
    start: currentPage * pageSize + 1,
    end: Math.min((currentPage + 1) * pageSize, total),
  }
}
