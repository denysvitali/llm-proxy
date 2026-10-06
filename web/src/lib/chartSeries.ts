import type { HistoryFormatter, HistorySeries } from '../components/HistoryCharts'

type FormatterKey = 'count' | 'percent' | 'seconds' | 'tps'

export function tpsSeries(formatters: Record<FormatterKey, HistoryFormatter>): HistorySeries[] {
  return [{ name: 'series0', label: 'Tokens/sec', formatter: formatters.tps }]
}

export function tokenMixSeries(formatters: Record<FormatterKey, HistoryFormatter>): HistorySeries[] {
  return [
    { name: 'series0', label: 'Input', formatter: formatters.count },
    { name: 'series1', label: 'Output', formatter: formatters.count },
  ]
}
