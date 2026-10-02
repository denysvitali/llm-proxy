// Typed client for llm-proxy's JSON APIs. Field names match the
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
  signal?: AbortSignal,
): Promise<ScopedStatsSeriesResponse> {
  const path = model
    ? `/api/stats/backends/${encodeURIComponent(backend)}/${encodeURIComponent(model)}`
    : `/api/stats/backends/${encodeURIComponent(backend)}`
  const data = await getJSON<NullableStatsSeriesResponse>(`${path}?range=${encodeURIComponent(range)}`, 'GET', signal)
  return normalizeStatsSeries(data)
}

type NullableStatsSeriesResponse = {
  models?: string[] | null
  series?: { [Key in keyof StatsSeries]?: StatsSeries[Key] | null } | null
}

function normalizeStatsSeries(data: NullableStatsSeriesResponse): StatsSeriesResponse {
  return {
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
  minimaxUsage?: GrokUsageMetadata
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

export interface MiniMaxQuotaWindow {
  remaining_percent?: number
  reset_at_ms?: number
  unlimited: boolean
}

export interface MiniMaxVideoQuota {
  remaining_count?: number
  total_count?: number
  reset_at_ms?: number
  unlimited: boolean
}

export interface MiniMaxCheckinDay {
  day_no: number
  points: number
  bonus_points?: number
  status: 1 | 2 | 3 | 4
  is_today: boolean
}

export interface MiniMaxCheckinPanel {
  scene: number
  days: MiniMaxCheckinDay[]
}

export interface MiniMaxUsage {
  account?: {
    has_token_plan?: boolean
    tier?: string
    expires_at_ms?: number
    credit_balance?: string
    quota_state: 'available' | 'not-subscribed' | 'unavailable'
    quota?: {
      five_hour: MiniMaxQuotaWindow
      weekly: MiniMaxQuotaWindow
      video?: MiniMaxVideoQuota
    }
  }
  checkin?: MiniMaxCheckinPanel
  accountError?: string
  checkinError?: string
  fetchedAt: string
}

export interface MiniMaxCheckinResult {
  claim_result: 1 | 2
  day_no: number
  points: number
  expire_at_ms: number
  panel: MiniMaxCheckinPanel
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

async function getJSON<T>(url: string, method: 'GET' | 'POST' = 'GET', signal?: AbortSignal): Promise<T> {
  const timeout = AbortSignal.timeout(method === 'POST' ? 60_000 : 10_000)
  const requestSignal = signal ? AbortSignal.any([signal, timeout]) : timeout
  const checkAborted = () => {
    // Preserve query cancellation instead of turning it into a retryable error.
    signal?.throwIfAborted()
    if (timeout.aborted) throw new ApiError(`${url}: request timed out`, 0, 'timeout')
  }

  let res: Response
  try {
    res = await fetch(url, {
      method,
      signal: requestSignal,
      ...(method === 'POST' ? { headers: { 'Content-Type': 'application/json' }, body: '{}' } : {}),
    })
  } catch (e) {
    checkAborted()
    throw new ApiError(`${url}: ${e instanceof Error ? e.message : 'network error'}`, 0, 'network')
  }

  if (!res.ok) {
    let message = `${url}: HTTP ${res.status}`
    try {
      const text = await res.text()
      if (text) {
        message = text
        try {
          const body = JSON.parse(text)
          const detail = typeof body.error === 'string' ? body.error : body.error?.message
          if (typeof detail === 'string') message = detail
          else if (url.startsWith('/api/minimax-code/')) {
            message = [body.accountError, body.checkinError].filter((part) => typeof part === 'string').join(' ') || 'MiniMax account information is temporarily unavailable.'
          }
        } catch {
          // Plain-text error responses are already readable.
        }
      }
    } catch {
      checkAborted()
      // An unreadable error body still has a useful HTTP status.
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
    checkAborted()
    throw new ApiError(`${url}: ${e instanceof Error ? e.message : 'parse error'}`, res.status, 'parse')
  }
}

export async function fetchStats(signal?: AbortSignal): Promise<StatsResponse> {
  const data = await getJSON<StatsResponse>('/stats', 'GET', signal)
  return { ...data, models: data.models ?? [] }
}

export async function fetchStatsSeries(range: string, signal?: AbortSignal): Promise<StatsSeriesResponse> {
  const data = await getJSON<NullableStatsSeriesResponse>(`/api/stats?range=${encodeURIComponent(range)}`, 'GET', signal)
  return normalizeStatsSeries(data)
}

export async function fetchOverview(signal?: AbortSignal): Promise<Overview> {
  const data = await getJSON<Overview>('/api/overview', 'GET', signal)
  return { ...data, backends: data.backends ?? [], routes: data.routes ?? [] }
}

export async function fetchGrokUsage(signal?: AbortSignal): Promise<GrokUsage> {
  const data = await getJSON<GrokUsage>('/api/grok/usage', 'GET', signal)
  return { ...data }
}

export async function fetchZcodeUsage(signal?: AbortSignal): Promise<ZcodeUsage> {
  // Go encodes an uninitialized slice as null. Normalize at the API boundary
  // so every consumer sees a collection, including during background refresh.
  const usage = await getJSON<Omit<ZcodeUsage, 'plans'> & { plans?: ZcodePlanUsage[] | null }>('/api/zcode/usage', 'GET', signal)
  return { ...usage, plans: usage.plans ?? [] }
}

export async function fetchMiniMaxUsage(signal?: AbortSignal): Promise<MiniMaxUsage> {
  const usage = await getJSON<MiniMaxUsage>('/api/minimax-code/usage', 'GET', signal)
  return {
    ...usage,
    checkin: usage.checkin ? { ...usage.checkin, days: usage.checkin.days ?? [] } : undefined,
  }
}

export async function claimMiniMaxCheckin(): Promise<MiniMaxCheckinResult> {
  // Claims are never retried automatically: a lost response may still have
  // awarded credits. The card refreshes the status after every attempt.
  const claim = await getJSON<MiniMaxCheckinResult>('/api/minimax-code/checkin', 'POST')
  return { ...claim, panel: { ...claim.panel, days: claim.panel?.days ?? [] } }
}

export async function fetchUpstreamErrors(signal?: AbortSignal): Promise<UpstreamErrorsResponse> {
  const data = await getJSON<UpstreamErrorsResponse>('/api/stats/errors', 'GET', signal)
  return { ...data, errors: data.errors ?? [] }
}

export async function fetchRequests(signal?: AbortSignal): Promise<RequestsResponse> {
  const data = await getJSON<RequestsResponse>('/api/requests', 'GET', signal)
  return { ...data, requests: data.requests ?? [] }
}

export function fetchRequest(id: string, signal?: AbortSignal): Promise<InspectedRequest> {
  return getJSON<InspectedRequest>(`/api/requests/${encodeURIComponent(id)}`, 'GET', signal)
}
