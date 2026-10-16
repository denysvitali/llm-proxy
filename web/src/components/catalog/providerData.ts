import type { ModelStat, OverviewRoute } from '../../api'
import { fmtPct } from '../../format.ts'

// Count every route row once, including duplicate routes and special backend names.
export function indexProviderRouteCounts(routes: readonly OverviewRoute[] | null | undefined) {
  const counts = new Map<string, number>()
  for (const route of routes ?? []) {
    counts.set(route.backend, (counts.get(route.backend) ?? 0) + 1)
  }
  return counts
}

// fmtPct renders an exact zero as an em dash, which would hide a genuine
// 0% success rate (every request failed) or a 0% tool error rate. Render a
// real zero honestly; the truly-absent cases ('—') are guarded by callers.
export function pct(ratio: number, digits = 1): string {
  if (ratio === 0) return `${(0).toFixed(digits)}%`
  return fmtPct(ratio, digits)
}

export type StatsState = 'ready' | 'loading' | 'unavailable'

function emptyAggregate() {
  return {
    requests: 0,
    successes: 0,
    uptime: 0,
    toolCalls: 0,
    toolErrors: 0,
    statusCodes: {} as Record<string, number>,
  }
}

export type ProviderAggregate = ReturnType<typeof emptyAggregate>

function addModel(aggregate: ProviderAggregate, model: ModelStat) {
  aggregate.requests += model.requests
  aggregate.successes += model.successes
  aggregate.toolCalls += model.tool_calls
  aggregate.toolErrors += model.tool_errors
  for (const [code, count] of Object.entries(model.status_codes ?? {})) {
    aggregate.statusCodes[code] = (aggregate.statusCodes[code] ?? 0) + count
  }
}

// Preserve row order and identity for the detail table. Map also handles backend
// names such as "__proto__" without collisions, and duplicate configured names
// reuse the same entry instead of counting their traffic twice.
export function indexProviderUsage(models: ModelStat[]) {
  const index = new Map<string, { models: ModelStat[]; aggregate: ProviderAggregate }>()
  for (const model of models) {
    let entry = index.get(model.backend)
    if (!entry) {
      entry = { models: [], aggregate: emptyAggregate() }
      index.set(model.backend, entry)
    }
    entry.models.push(model)
    addModel(entry.aggregate, model)
  }
  for (const { aggregate } of index.values()) {
    aggregate.uptime = aggregate.requests ? aggregate.successes / aggregate.requests : 0
  }
  return index
}

export function emptyProviderUsage() {
  return { models: [] as ModelStat[], aggregate: emptyAggregate() }
}

// Kept for callers aggregating a single provider, without temporary filters or
// the repeated reductions previously used for every field.
export function backendAgg(models: ModelStat[], backendName: string) {
  const aggregate = emptyAggregate()
  for (const model of models) {
    if (model.backend === backendName) addModel(aggregate, model)
  }
  aggregate.uptime = aggregate.requests ? aggregate.successes / aggregate.requests : 0
  return aggregate
}
