// Run: node --experimental-strip-types web/tests/provider-performance.bench.mjs
// Pure data preparation timings, NOT browser rendering or network latency.
// Both implementations consume identical synthetic rows; median/p95 of 25 runs
// after 5 warmups. No timers are asserted by the correctness tests.
import assert from 'node:assert/strict'
import { performance } from 'node:perf_hooks'
import { indexProviderUsage } from '../src/components/catalog/providerData.ts'
import { providerSegments } from '../src/lib/stats.ts'
import { baselineAggregate, baselineProviders, baselineSegments, dataset } from './fixtures/provider-baseline.mjs'

const colors = ['red', 'green', 'blue', 'yellow']
let sink
function measure(run) {
  for (let i = 0; i < 5; i++) sink = run()
  const samples = Array.from({ length: 25 }, () => {
    const start = performance.now()
    sink = run()
    return performance.now() - start
  }).sort((a, b) => a - b)
  return { median_ms: +samples[12].toFixed(3), p95_ms: +samples[23].toFixed(3) }
}

for (const [count, perBackend] of [[20, 100], [200, 100], [20, 1000]]) {
  const { models, backends } = dataset(count, perBackend)
  const oldProviders = baselineProviders(models, backends)
  const index = indexProviderUsage(models)
  const providers = backends.map((name) => index.get(name))
  assert.deepEqual(providers, oldProviders)
  assert.deepEqual(providerSegments(models, colors), baselineSegments(models, colors))
  const beforeRefresh = () => {
    const rows = baselineProviders(models, backends)
    const segments = baselineSegments(models, colors)
    // Cards used to aggregate the same provider rows once more on each render.
    return [rows.map((entry, i) => baselineAggregate(entry.models, backends[i])), segments]
  }
  const afterRefresh = () => {
    const usage = indexProviderUsage(models)
    return [backends.map((name) => usage.get(name).aggregate), providerSegments(models, colors)]
  }
  console.log(JSON.stringify({ backends: count, models: models.length,
    refresh_data_before: measure(beforeRefresh), refresh_data_after: measure(afterRefresh),
    card_data_rerender_before: measure(() => oldProviders.map((entry, i) => baselineAggregate(entry.models, backends[i]))),
    card_data_rerender_after: measure(() => providers.map((entry) => entry.aggregate)),
    token_mix_before: measure(() => baselineSegments(models, colors)),
    token_mix_after: measure(() => providerSegments(models, colors)),
  }))
}
assert.ok(sink)
