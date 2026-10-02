import { queryOptions, type QueryClient } from '@tanstack/react-query'
import {
  ApiError, fetchBackendStatsSeries, fetchGrokUsage, fetchMiniMaxUsage,
  fetchOverview, fetchRequests, fetchStats, fetchStatsSeries,
  fetchUpstreamErrors, fetchZcodeUsage,
} from './api.ts'

const retryTransient = (limit: number) => (failureCount: number, error: Error) =>
  failureCount < limit && error instanceof ApiError &&
  (error.kind === 'network' || error.kind === 'timeout' ||
    (error.kind === 'http' && (error.status === 429 || error.status >= 500)))

export const dashboardQueryDefaults = {
  staleTime: 30_000,
  gcTime: 5 * 60_000,
  retry: retryTransient(2),
  refetchOnWindowFocus: false,
  placeholderData: (previous: unknown) => previous,
}

export const queryKeys = {
  overview: ['overview'],
  stats: ['stats'],
  series: ['stats-series'],
  errors: ['upstream-errors'],
  requests: ['recent-requests'],
  grokUsage: ['grok-usage'],
  zcodeUsage: ['zcode-usage'],
  minimaxUsage: ['minimax-usage'],
} as const

const usageOptions = { refetchInterval: 60_000, retry: retryTransient(1) }

// Query keys, transport cancellation, and refresh policy live here so every
// page observing the same resource uses the same contract.
export const dashboardQueries = {
  overview: () => queryOptions({
    queryKey: queryKeys.overview,
    queryFn: ({ signal }) => fetchOverview(signal),
  }),
  stats: () => queryOptions({
    queryKey: queryKeys.stats,
    queryFn: ({ signal }) => fetchStats(signal),
  }),
  series: (range: string) => queryOptions({
    queryKey: [...queryKeys.series, range],
    queryFn: ({ signal }) => fetchStatsSeries(range, signal),
  }),
  backendSeries: (backend: string | null, range: string) => queryOptions({
    queryKey: [...queryKeys.series, 'backend', backend, range],
    queryFn: ({ signal }) => fetchBackendStatsSeries(backend!, range, undefined, signal),
    enabled: backend !== null,
    // A newly selected provider must not temporarily show another one's data.
    placeholderData: undefined,
  }),
  modelSeries: (backend: string | undefined, model: string | undefined, range: string) => queryOptions({
    queryKey: [...queryKeys.series, 'model', backend, model, range],
    queryFn: ({ signal }) => fetchBackendStatsSeries(backend!, range, model, signal),
    enabled: backend !== undefined && model !== undefined,
    placeholderData: undefined,
  }),
  errors: () => queryOptions({
    queryKey: queryKeys.errors,
    queryFn: ({ signal }) => fetchUpstreamErrors(signal),
    refetchInterval: 30_000,
    retry: retryTransient(1),
  }),
  requests: () => queryOptions({
    queryKey: queryKeys.requests,
    queryFn: ({ signal }) => fetchRequests(signal),
    refetchInterval: 30_000,
  }),
  grokUsage: (enabled: boolean) => queryOptions({
    ...usageOptions,
    queryKey: queryKeys.grokUsage,
    queryFn: ({ signal }) => fetchGrokUsage(signal),
    enabled,
  }),
  zcodeUsage: (enabled: boolean) => queryOptions({
    ...usageOptions,
    queryKey: queryKeys.zcodeUsage,
    queryFn: ({ signal }) => fetchZcodeUsage(signal),
    enabled,
  }),
  minimaxUsage: (enabled: boolean) => queryOptions({
    ...usageOptions,
    queryKey: queryKeys.minimaxUsage,
    queryFn: ({ signal }) => fetchMiniMaxUsage(signal),
    enabled,
  }),
}

const liveQueryRoots = new Set<string>([
  queryKeys.stats[0], queryKeys.series[0], queryKeys.errors[0], queryKeys.requests[0],
  queryKeys.grokUsage[0], queryKeys.zcodeUsage[0], queryKeys.minimaxUsage[0],
])

export function refreshLiveQueries(queryClient: QueryClient) {
  // Let an in-flight refresh finish even when the next event batch arrives.
  return queryClient.invalidateQueries({
    predicate: ({ queryKey }) => liveQueryRoots.has(String(queryKey[0])),
  }, { cancelRefetch: false })
}
