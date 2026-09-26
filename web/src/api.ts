// Typed client for llm-proxy's read-only JSON APIs. Field names match the
// Go structs' json tags exactly (internal/server/stats.go, dashboard.go).

export interface Percentiles {
  p50: number
  p90: number
  p99: number
}

export interface ModelStat {
  backend: string
  model: string
  requests: number
  successes: number
  uptime: number
  ttft_seconds: Percentiles
  e2e_seconds: Percentiles
  throughput_tps: Percentiles
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cache_rate: number
  tool_calls: number
  tool_errors: number
  tool_error_rate: number
  status_codes?: Record<string, number>
}

export interface UpstreamErrorEvent {
  at: string
  backend: string
  model: string
  status: string // HTTP code as text; "error" = the request never got a response
  message?: string
  request_id?: string
}

export interface InspectedRequest {
  id: string
  at: string
  proxy_request_id?: string
  backend: string
  model: string
  kind?: string
  status: string
  error?: string
}

export interface RequestsResponse { requests: InspectedRequest[] }

export interface UpstreamErrorsResponse {
  errors: UpstreamErrorEvent[]
}

export interface StatsResponse {
  models: ModelStat[]
}

export interface SeriesPoint {
  ts: string
  value: number
}

export interface StatsSeries {
  requests: SeriesPoint[]
  success_rate: SeriesPoint[]
  ttft_p50: SeriesPoint[]
  e2e_p50: SeriesPoint[]
  throughput_p50: SeriesPoint[]
  tokens_in: SeriesPoint[]
  tokens_out: SeriesPoint[]
  tool_calls: SeriesPoint[]
  tool_errors: SeriesPoint[]
}

export interface StatsSeriesResponse {
  models: string[]
  series: StatsSeries
}

export type ScopedStatsSeriesResponse = StatsSeriesResponse

export async function fetchBackendStatsSeries(
  backend: string,
  range: string,
  model?: string,
): Promise<ScopedStatsSeriesResponse> {
  const path = model
    ? `/api/stats/backends/${encodeURIComponent(backend)}/${encodeURIComponent(model)}`
    : `/api/stats/backends/${encodeURIComponent(backend)}`
  const data = await getJSON<ScopedStatsSeriesResponse>(`${path}?range=${encodeURIComponent(range)}`)
  return {
    ...data,
    models: data.models ?? [],
    series: {
      requests: data.series?.requests ?? [],
      success_rate: data.series?.success_rate ?? [],
      ttft_p50: data.series?.ttft_p50 ?? [],
      e2e_p50: data.series?.e2e_p50 ?? [],
      throughput_p50: data.series?.throughput_p50 ?? [],
      tokens_in: data.series?.tokens_in ?? [],
      tokens_out: data.series?.tokens_out ?? [],
      tool_calls: data.series?.tool_calls ?? [],
      tool_errors: data.series?.tool_errors ?? [],
    },
  }
}

export interface OverviewBackend {
  name: string
  enabled: boolean
  host: string
  hasKey: boolean
  authLabel: string
  authConfigured: boolean
  models: string[] | null
  modelCredits?: Record<string, string>
  catalogOK: boolean
}

export interface OverviewRoute {
  model: string
  backend: string
  upstream: string
}

export interface Overview {
  name: string
  version: string
  listen: string
  authEnabled: boolean
  backends: OverviewBackend[]
  routes: OverviewRoute[]
  stats?: ModelStat[]
  grokUsage: GrokUsageMetadata
  zcodeUsage: ZcodeUsageMetadata
  hasDefault: boolean
  defaultRoute: OverviewRoute
  exampleModel: string
  claudeSnippet: string
  codexSnippet: string
}

export interface GrokUsageMetadata {
  configured: boolean
  available: boolean
  error?: string
}

export type ZcodeUsageMetadata = GrokUsageMetadata

export interface ZcodePlanUsage {
  plan_id: string
  name?: string
  status?: string
  kind?: string
  reason?: string
  total_units?: number
  used_units?: number
  available_units?: number
  period_start?: number
  period_end?: number
}

export interface ZcodeUsage {
  plans: ZcodePlanUsage[]
  fetchedAt: string
}

export interface GrokUsage {
  available: boolean
  email?: string
  name?: string
  subscriptionTier?: string
  percentUsed: number
  hasPercent: boolean
  limitCents?: number
  usedCents?: number
  remainingCents?: number
  onDemandUsedCents?: number
  onDemandCapCents?: number
  prepaidCents?: number
  periodType?: string
  periodStart?: string
  periodEnd?: string
  fetchedAt: string
}

export class ApiError extends Error {
  status: number
  kind: 'network' | 'http' | 'timeout' | 'parse'

  constructor(
    message: string,
    status: number,
    kind: 'network' | 'http' | 'timeout' | 'parse',
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.kind = kind
  }
}

async function getJSON<T>(url: string): Promise<T> {
  const doFetch = async (): Promise<T> => {
    let res: Response
    try {
      res = await fetch(url, { signal: AbortSignal.timeout(10_000) })
    } catch (e) {
      if (e instanceof DOMException && e.name === 'TimeoutError') {
        throw new ApiError(`${url}: request timed out`, 0, 'timeout')
      }
      throw new ApiError(`${url}: ${e instanceof Error ? e.message : 'network error'}`, 0, 'network')
    }

    if (!res.ok) {
      let message = `${url}: HTTP ${res.status}`
      try {
        const text = await res.text()
        if (text) message = text
      } catch {
        // ignore body read errors
      }
      throw new ApiError(message, res.status, 'http')
    }

    const contentType = res.headers.get('content-type') ?? ''
    if (!contentType.includes('application/json')) {
      throw new ApiError(`${url}: expected JSON, got ${contentType || 'unknown content-type'}`, res.status, 'parse')
    }

    try {
      return (await res.json()) as T
    } catch (e) {
      throw new ApiError(`${url}: ${e instanceof Error ? e.message : 'parse error'}`, res.status, 'parse')
    }
  }

  let lastError: unknown
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      return await doFetch()
    } catch (e) {
      lastError = e
      if (e instanceof ApiError && (e.kind === 'network' || e.status >= 500) && attempt < 1) {
        await new Promise(r => setTimeout(r, 500 * (attempt + 1)))
        continue
      }
      throw e
    }
  }
  throw lastError
}

export async function fetchStats(): Promise<StatsResponse> {
  const data = await getJSON<StatsResponse>('/stats')
  return { ...data, models: data.models ?? [] }
}

export function fetchStatsSeries(range: string): Promise<StatsSeriesResponse> {
  return getJSON<StatsSeriesResponse>(`/api/stats?range=${encodeURIComponent(range)}`)
}

export async function fetchOverview(): Promise<Overview> {
  const data = await getJSON<Overview>('/api/overview')
  return { ...data, backends: data.backends ?? [], routes: data.routes ?? [] }
}

export async function fetchGrokUsage(): Promise<GrokUsage> {
  const data = await getJSON<GrokUsage>('/api/grok/usage')
  return { ...data }
}

export async function fetchZcodeUsage(): Promise<ZcodeUsage> {
  // Go encodes an uninitialized slice as null. Normalize at the API boundary
  // so every consumer sees a collection, including during background refresh.
  const usage = await getJSON<Omit<ZcodeUsage, 'plans'> & { plans?: ZcodePlanUsage[] | null }>('/api/zcode/usage')
  return { ...usage, plans: usage.plans ?? [] }
}

export async function fetchUpstreamErrors(): Promise<UpstreamErrorsResponse> {
  const data = await getJSON<UpstreamErrorsResponse>('/api/stats/errors')
  return { ...data, errors: data.errors ?? [] }
}

export async function fetchRequests(): Promise<RequestsResponse> {
  const data = await getJSON<RequestsResponse>('/api/requests')
  return { ...data, requests: data.requests ?? [] }
}

export function fetchRequest(id: string): Promise<InspectedRequest> {
  return getJSON<InspectedRequest>(`/api/requests/${encodeURIComponent(id)}`)
}
