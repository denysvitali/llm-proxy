import { ApiError, type InspectedRequest } from '../api.ts'

// What the request-inspection modal shows. The detail query only runs while
// the modal is open; the view is derived from its status, first match wins:
//
//   loading    no result yet (also while closed: the query is disabled)
//   expired    the server answered 404: the request left retained history
//   failed     any other error; offers a retry
//   loaded     the request detail
//   empty      settled without data (not expected in practice)
export type RequestInspectView =
  | { kind: 'loading' }
  | { kind: 'expired' }
  | { kind: 'failed' }
  | { kind: 'loaded'; request: InspectedRequest }
  | { kind: 'empty' }

export function requestInspectView(query: { isPending: boolean; isError: boolean; error: unknown; data?: InspectedRequest }): RequestInspectView {
  if (query.isPending) return { kind: 'loading' }
  if (query.isError) return query.error instanceof ApiError && query.error.status === 404 ? { kind: 'expired' } : { kind: 'failed' }
  return query.data ? { kind: 'loaded', request: query.data } : { kind: 'empty' }
}
