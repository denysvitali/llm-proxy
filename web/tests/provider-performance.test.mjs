import assert from 'node:assert/strict'
import { test } from 'node:test'
import { QueryClient } from '@tanstack/react-query'
import { backendAgg, emptyProviderUsage, indexProviderRouteCounts, indexProviderUsage, pct } from '../src/components/catalog/providerData.ts'
import { providerSegments } from '../src/lib/stats.ts'
import { baselineAggregate, baselineProviders, baselineSegments, dataset } from './fixtures/provider-baseline.mjs'

const colors = ['red', 'green', 'blue', 'yellow']

test('route counts match per-provider filtering with duplicate routes and special names', () => {
  const names = ['alpha', 'alpha', '__proto__', 'constructor', '', 'missing']
  const routes = Object.freeze(['alpha', 'alpha', '__proto__', 'constructor', '', 'other']
    .map((backend) => Object.freeze({ backend })))
  const counts = indexProviderRouteCounts(routes)
  for (const name of names) {
    assert.equal(counts.get(name) ?? 0, routes.filter((route) => route.backend === name).length)
  }
  for (const empty of [null, undefined, []]) assert.equal(indexProviderRouteCounts(empty).size, 0)
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: 0 } } })
  client.setQueryData(['overview'], { routes, revision: 1 })
  const before = client.getQueryData(['overview'])
  client.setQueryData(['overview'], { routes: structuredClone(routes), revision: 2 })
  assert.equal(client.getQueryData(['overview']).routes, before.routes, 'unchanged routes retain the memo key')
  client.clear()
})

test('indexed usage exactly matches the previous aggregation including duplicate and missing backends', () => {
  const { backends, models } = dataset(17, 31)
  backends.push('missing', backends[0], '__proto__', 'constructor', '')
  models.push({ ...models[0], backend: '__proto__' }, { ...models[1], backend: 'constructor' },
    { ...models[2], backend: '' }, { ...models[3], backend: 'not-configured', status_codes: undefined })
  for (const model of models) Object.freeze(model)
  Object.freeze(models)
  const index = indexProviderUsage(models)
  assert.deepEqual(backends.map((name) => index.get(name) ?? emptyProviderUsage()), baselineProviders(models, backends))
  for (const name of backends) assert.deepEqual(backendAgg(models, name), baselineAggregate(models, name))
  for (const [name, entry] of index) {
    const expected = models.filter((model) => model.backend === name)
    entry.models.forEach((model, i) => assert.equal(model, expected[i], 'retain identity/order and latency/throughput fields'))
  }
  assert.deepEqual(providerSegments(models, colors), baselineSegments(models, colors))
})

test('empty usage, real zero and unavailable statistics remain distinct', () => {
  assert.equal(indexProviderUsage([]).size, 0)
  assert.deepEqual(providerSegments([], colors), [])
  assert.deepEqual(emptyProviderUsage().aggregate, baselineAggregate([], 'missing'))
  assert.equal(pct(0), '0.0%')
  const zero = { ...dataset(1, 1).models[0], requests: 0, successes: 0 }
  assert.equal(indexProviderUsage([zero]).get(zero.backend).aggregate.uptime, 0)
  assert.notEqual(emptyProviderUsage(), emptyProviderUsage(), 'no shared mutable empty rows')
})

test('single-pass token totals preserve fractional values, input order and absent palettes', () => {
  const { models } = dataset(3, 11)
  models.forEach((model, i) => { model.input_tokens = i / 7; model.output_tokens = i / 13 })
  assert.deepEqual(providerSegments(models.reverse(), []), baselineSegments(models, []))
})

test('unchanged model rows survive refresh envelope changes through structural sharing', () => {
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: 0 } } })
  const { models } = dataset(3, 5)
  client.setQueryData(['stats'], { models, requests: 1 })
  const before = client.getQueryData(['stats'])
  client.setQueryData(['stats'], { models: structuredClone(models), requests: 2 })
  const after = client.getQueryData(['stats'])
  assert.notEqual(after, before)
  assert.equal(after.models, before.models, 'models-based memo avoids re-aggregation when only totals change')
  client.clear()
})
