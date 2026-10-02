import type { ModelStat } from '../../api'
import { clampRate } from '../../format'

export type SortKey =
  'model' | 'requests' | 'uptime' | 'ttft' | 'e2e' | 'tps' | 'cache' | 'tools' | 'toolErr' | 'success'
export type ModelSort = { key: SortKey; dir: 1 | -1 }

export const sortOptions = [
  { value: 'requests', label: 'Most requests' },
  { value: 'model', label: 'Model name' },
  { value: 'ttft', label: 'First token latency' },
  { value: 'e2e', label: 'Response latency' },
  { value: 'tps', label: 'Throughput' },
  { value: 'success', label: 'Success rate' },
  { value: 'cache', label: 'Cache hit rate' },
  { value: 'tools', label: 'Tool calls' },
  { value: 'toolErr', label: 'Tool error rate' },
]

export function observedRate(rate: number, observations: number): string {
  return observations > 0 && Number.isFinite(rate) ? `${(clampRate(rate) * 100).toFixed(1)}%` : '—'
}

export function hasModelErrors(model: ModelStat) {
  return model.requests - model.successes > 0 || model.tool_errors > 0
}

export function sortValue(model: ModelStat, key: SortKey): string | number {
  switch (key) {
    case 'model':
      return `${model.model}/${model.backend}`
    case 'requests':
      return model.requests
    case 'uptime':
      return model.requests > 0 ? model.uptime : NaN
    case 'ttft':
      return model.ttft_seconds.p50 > 0 ? model.ttft_seconds.p50 : NaN
    case 'e2e':
      return model.e2e_seconds.p50 > 0 ? model.e2e_seconds.p50 : NaN
    case 'tps':
      return model.throughput_tps.p50 > 0 ? model.throughput_tps.p50 : NaN
    case 'cache':
      return model.input_tokens + model.cache_read_tokens + model.cache_write_tokens > 0
        ? model.cache_rate
        : NaN
    case 'tools':
      return model.tool_calls
    case 'toolErr':
      return model.tool_calls > 0 ? model.tool_error_rate : NaN
    case 'success':
      return model.requests > 0 ? model.successes / model.requests : NaN
  }
}

export function compareModels(a: ModelStat, b: ModelStat, sort: ModelSort) {
  const av = sortValue(a, sort.key)
  const bv = sortValue(b, sort.key)
  if (typeof av === 'string' || typeof bv === 'string') return String(av).localeCompare(String(bv)) * sort.dir
  // Unknown measurements stay at the end in both sort directions.
  if (!Number.isFinite(av)) return Number.isFinite(bv) ? 1 : 0
  if (!Number.isFinite(bv)) return -1
  return (av - bv) * sort.dir
}
