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
}

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

export function fetchBackendStatsSeries(
  backend: string,
  range: string,
  model?: string,
): Promise<ScopedStatsSeriesResponse> {
  const path = model
    ? `/api/stats/backends/${encodeURIComponent(backend)}/${encodeURIComponent(model)}`
    : `/api/stats/backends/${encodeURIComponent(backend)}`
  return getJSON<ScopedStatsSeriesResponse>(`${path}?range=${encodeURIComponent(range)}`)
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

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`)
  return (await res.json()) as T
}

export function fetchStats(): Promise<StatsResponse> {
  return getJSON<StatsResponse>('/stats')
}

export function fetchStatsSeries(range: string): Promise<StatsSeriesResponse> {
  return getJSON<StatsSeriesResponse>(`/api/stats?range=${encodeURIComponent(range)}`)
}

export function fetchOverview(): Promise<Overview> {
  return getJSON<Overview>('/api/overview')
}

export function fetchGrokUsage(): Promise<GrokUsage> {
  return getJSON<GrokUsage>('/api/grok/usage')
}

export function fetchUpstreamErrors(): Promise<UpstreamErrorsResponse> {
  return getJSON<UpstreamErrorsResponse>('/api/stats/errors')
}
