import assert from 'node:assert/strict'
import { test } from 'node:test'
import { QueryClient, QueryObserver } from '@tanstack/react-query'
import {
  ApiError, claimMiniMaxCheckin, fetchBackendStatsSeries, fetchStats, fetchStatsSeries, fetchZcodeQuota,
} from '../src/api.ts'
import { dashboardQueries, dashboardQueryDefaults, queryKeys, refreshLiveQueries } from '../src/queries.ts'
import { createLiveRefreshScheduler } from '../src/liveRefresh.ts'

const emptySeries = Object.fromEntries([
  'requests', 'success_rate', 'ttft_p50', 'e2e_p50', 'throughput_p50',
  'tokens_in', 'tokens_out', 'tool_calls', 'tool_errors',
].map((key) => [key, []]))

function queryClient(t) {
  const client = new QueryClient({ defaultOptions: {
    queries: { ...dashboardQueryDefaults, retryDelay: 0, gcTime: 0 },
  } })
  t.after(() => client.clear())
  return client
}

test('global and scoped histories normalize null, absent, and partial series equally', async (t) => {
  const point = { ts: '2030-01-01T00:00:00Z', value: 4 }
  let body = {}
  t.mock.method(globalThis, 'fetch', async () => Response.json(body))
  for (const value of [{}, { models: null, series: null }, { series: { requests: null } },
    { models: ['example'], series: { requests: [point], tool_errors: null } }]) {
    body = value
    const expected = { models: value.models ?? [], series: { ...emptySeries, requests: value.series?.requests ?? [] } }
    assert.deepEqual(await fetchStatsSeries('24h'), expected)
    assert.deepEqual(await fetchBackendStatsSeries('example', '24h'), expected)
    assert.deepEqual(await fetchBackendStatsSeries('example', '24h', 'example/model'), expected)
  }
})

test('idle intervals carry no ratio or percentile sample', async (t) => {
  const idle = '2030-01-01T00:00:00Z'
  const busy = '2030-01-01T01:00:00Z'
  const pair = (a, b) => [{ ts: idle, value: a }, { ts: busy, value: b }]
  t.mock.method(globalThis, 'fetch', async () => Response.json({ series: {
    requests: pair(0, 4), success_rate: pair(0, 0.75), e2e_p50: pair(0, 0.2),
    ttft_p50: pair(0, 0.1), throughput_p50: pair(0, 40), tokens_in: pair(0, 12),
  } }))
  const { series } = await fetchStatsSeries('24h')
  for (const key of ['success_rate', 'e2e_p50', 'ttft_p50', 'throughput_p50']) {
    assert.equal(series[key][0].ts, idle, `${key} keeps the idle timestamp`)
    assert.ok(Number.isNaN(series[key][0].value), `${key} idle value is not a measured zero`)
    assert.ok(series[key][1].value > 0)
  }
  assert.deepEqual(series.tokens_in, pair(0, 12), 'counts keep their real zeros')
})

test('transport does not retry a failed read or an ambiguous check-in', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ error: 'unavailable' }, { status: 503 }))
  await assert.rejects(fetchStats(), { status: 503 })
  assert.equal(fetch.mock.callCount(), 1)
  await assert.rejects(claimMiniMaxCheckin(), { status: 503 })
  assert.equal(fetch.mock.callCount(), 2)
})

test('query policy is the only retry owner for transient failures', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ error: 'unavailable' }, { status: 503 }))
  await assert.rejects(queryClient(t).fetchQuery(dashboardQueries.stats()), { status: 503 })
  assert.equal(fetch.mock.callCount(), 3, 'initial read and exactly two retries')
})

test('usage query policy allows only one retry', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => Response.json({ error: 'unavailable' }, { status: 503 }))
  await assert.rejects(queryClient(t).fetchQuery(dashboardQueries.minimaxUsage(true)), { status: 503 })
  assert.equal(fetch.mock.callCount(), 2)
})

test('ZCode quota normalizes nullable collections without inventing counters', async (t) => {
  let body = {}
  t.mock.method(globalThis, 'fetch', async (url) => {
    assert.equal(url, '/api/zcode/quota')
    return Response.json(body)
  })
  for (const value of [{}, { plans: null, balances: null }]) {
    body = value
    assert.deepEqual(await fetchZcodeQuota(), { plans: [], balances: [] })
  }
  body = { plans: [{ plan_id: 'free' }], balances: [
    { remaining_units: 0, reserved_units: 0, capabilities: null },
    { available_units: 50 },
  ] }
  const quota = await fetchZcodeQuota()
  assert.deepEqual(quota.plans, body.plans)
  assert.deepEqual(quota.balances, [
    { remaining_units: 0, reserved_units: 0, capabilities: [] },
    { available_units: 50, capabilities: [] },
  ])
  assert.equal(quota.balances[1].remaining_units, undefined)
  assert.equal(quota.balances[1].reserved_units, undefined)
})

test('permanent HTTP failures and invalid JSON are not retried', async (t) => {
  let response = () => Response.json({ error: 'not authorized' }, { status: 401 })
  const fetch = t.mock.method(globalThis, 'fetch', async () => response())
  const client = queryClient(t)
  await assert.rejects(client.fetchQuery(dashboardQueries.stats()), { status: 401 })
  assert.equal(fetch.mock.callCount(), 1)
  response = () => new Response('not json', { headers: { 'Content-Type': 'application/json' } })
  await assert.rejects(client.fetchQuery(dashboardQueries.stats()), { kind: 'parse' })
  assert.equal(fetch.mock.callCount(), 2)
})

test('query cancellation reaches the transport without becoming a retryable network error', async (t) => {
  let requestSignal
  let started
  const ready = new Promise((resolve) => { started = resolve })
  const fetch = t.mock.method(globalThis, 'fetch', (_, { signal }) => new Promise((_, reject) => {
    requestSignal = signal
    signal.addEventListener('abort', () => reject(signal.reason), { once: true })
    started()
  }))
  const client = queryClient(t)
  const result = client.fetchQuery(dashboardQueries.stats()).catch((error) => error)
  await ready
  await client.cancelQueries({ queryKey: queryKeys.stats })
  assert.equal(requestSignal.aborted, true)
  assert.equal((await result) instanceof ApiError, false)
  assert.equal(fetch.mock.callCount(), 1)
})

test('live invalidation preserves in-flight requests and includes every usage provider', async (t) => {
  const client = queryClient(t)
  client.setQueryData(queryKeys.stats, { models: [] })
  client.setQueryData(queryKeys.overview, {})
  for (const key of [queryKeys.grokUsage, queryKeys.zcodeQuota, queryKeys.minimaxUsage]) client.setQueryData(key, {})
  let complete
  let requestSignal
  const fetch = t.mock.method(globalThis, 'fetch', (_, { signal }) => new Promise((resolve) => {
    requestSignal = signal
    complete = () => resolve(Response.json({ models: [] }))
  }))
  const observer = new QueryObserver(client, dashboardQueries.stats())
  const unsubscribe = observer.subscribe(() => {})
  t.after(unsubscribe)
  const first = refreshLiveQueries(client)
  const second = refreshLiveQueries(client)
  assert.equal(fetch.mock.callCount(), 1)
  assert.equal(requestSignal.aborted, false, 'a later event must not restart a slow refresh')
  for (const key of [queryKeys.grokUsage, queryKeys.zcodeQuota, queryKeys.minimaxUsage]) {
    assert.equal(client.getQueryState(key).isInvalidated, true)
  }
  assert.equal(client.getQueryState(queryKeys.overview).isInvalidated, false)
  complete()
  await Promise.all([first, second])
})

test('continuous updates refresh on the first event deadline instead of starving', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let refreshes = 0
  // Default window is 150ms; under this 100ms event cadence the timer fires on
  // the first tick past each deadline (200, 400, ... 1400) — once per window,
  // never per event.
  const scheduler = createLiveRefreshScheduler(() => refreshes++)
  for (let time = 0; time < 1500; time += 100) {
    scheduler.schedule()
    t.mock.timers.tick(100)
  }
  assert.equal(refreshes, 7)
  scheduler.schedule()
  scheduler.cancel()
  t.mock.timers.tick(500)
  assert.equal(refreshes, 7, 'unmount cancels pending refresh')
})

test('visibility refresh clears the pending batch without a duplicate refresh', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let refreshes = 0
  const scheduler = createLiveRefreshScheduler(() => refreshes++)
  scheduler.schedule()
  t.mock.timers.tick(100)
  scheduler.flush()
  t.mock.timers.tick(500)
  assert.equal(refreshes, 1)
})

test('live request events prepend to the recent-requests cache, deduped and capped', async () => {
  const { injectLiveRequestIntoCache } = await import('../src/useLiveUpdates.ts')
  const existing = (id) => ({ id, at: '2026-10-21T07:00:00Z', backend: 'venice', model: 'm', status: '200' })
  const old = { requests: [existing('b'), existing('a')] }
  const live = { id: 'c', at: '2026-10-21T07:05:00Z', backend: 'venice', model: 'm', status: '429' }
  const first = injectLiveRequestIntoCache(live, old)
  assert.deepEqual(first.requests.map((entry) => entry.id), ['c', 'b', 'a'], 'live request lands newest first')
  const replays = injectLiveRequestIntoCache(live, first)
  assert.deepEqual(replays.requests.map((entry) => entry.id), ['c', 'b', 'a'], 'a replayed event dedupes by id')
  for (let i = 0; i < 60; i++) old.requests.push(existing(`r${i}`))
  const capped = injectLiveRequestIntoCache(live, old)
  assert.equal(capped.requests.length, 50, 'cache never grows past the cap')
  assert.equal(capped.requests[0].id, 'c')
  const empty = injectLiveRequestIntoCache(live, undefined)
  assert.deepEqual(empty.requests.map((entry) => entry.id), ['c'], 'missing cache is filled from empty')
})
