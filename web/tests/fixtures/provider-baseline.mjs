// Frozen pre-optimization algorithms: independent oracle and benchmark baseline.
export function baselineAggregate(models, backendName) {
  const ms = models.filter((m) => m.backend === backendName)
  const requests = ms.reduce((s, m) => s + m.requests, 0)
  const successes = ms.reduce((s, m) => s + m.successes, 0)
  const toolCalls = ms.reduce((s, m) => s + m.tool_calls, 0)
  const toolErrors = ms.reduce((s, m) => s + m.tool_errors, 0)
  const statusCodes = {}
  for (const m of ms) {
    for (const [code, n] of Object.entries(m.status_codes ?? {})) {
      statusCodes[code] = (statusCodes[code] ?? 0) + n
    }
  }
  return { requests, successes, uptime: requests ? successes / requests : 0, toolCalls, toolErrors, statusCodes }
}

export function baselineProviders(models, backends) {
  return backends.map((name) => {
    const rows = models.filter((model) => model.backend === name)
    return { models: rows, aggregate: baselineAggregate(rows, name) }
  })
}

export function baselineSegments(models, colors) {
  const kinds = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens']
  const names = ['input', 'output', 'cache read', 'cache write']
  const byBackend = new Map()
  for (const m of models) byBackend.set(m.backend, [...(byBackend.get(m.backend) ?? []), m])
  return [...byBackend.entries()].map(([backend, ms]) => [backend,
    kinds.map((key, i) => ({ name: names[i], color: colors[i], value: ms.reduce((sum, m) => sum + m[key], 0) })),
  ])
}

export function dataset(backendCount, modelsPerBackend) {
  const backends = Array.from({ length: backendCount }, (_, i) => `backend-${i}`)
  const models = backends.flatMap((backend) => Array.from({ length: modelsPerBackend }, (_, i) => ({
    backend, model: `model-${i}`, requests: i * 3, successes: i * 2,
    tool_calls: i, tool_errors: i % 3, status_codes: i % 2 ? { 200: i, 500: 1 } : null,
    input_tokens: i * 19, output_tokens: i * 7, cache_read_tokens: i * 2, cache_write_tokens: i,
    ttft_p50: i / 100, e2e_p50: i / 10, throughput_p50: i * 2,
  })))
  return { backends, models }
}
