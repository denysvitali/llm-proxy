import assert from 'node:assert/strict'

const percentile = { p50: 1, p90: 2, p99: 3 }
const model = (name, requests, successes) => ({
  backend: name,
  model: `${name}-model`,
  requests,
  successes,
  uptime: requests ? successes / requests : 0,
  ttft_seconds: percentile,
  e2e_seconds: percentile,
  throughput_tps: percentile,
  input_tokens: 0,
  output_tokens: 0,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
  cache_rate: 0,
  tool_calls: 0,
  tool_errors: 0,
  tool_error_rate: 0,
})
const backend = (name) => ({
  name,
  host: `${name}.invalid`,
  enabled: true,
  hasKey: true,
  authConfigured: true,
  catalogOK: true,
  models: Array.from({ length: 8 }, (_, i) => `${name}/model-${i}`),
})
const overview = {
  backends: [backend('alpha'), backend('beta')],
  routes: [],
  grokUsage: { configured: false },
  zcodeUsage: { configured: false },
  minimaxUsage: { configured: false },
}
// All dashboard APIs are intercepted; this suite never sends inference or changes configuration.
export async function checkCatalogRegressions(browser, base) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  try {
    await context.route(/\/(api\/|stats(?:\?|$))/, async (route) => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/overview') return route.fulfill({ json: overview })
      if (path === '/stats')
        return route.fulfill({ json: { models: [model('alpha', 10, 0), model('beta', 0, 0)] } })
      if (path.startsWith('/api/stats')) return route.fulfill({ json: { models: [], series: {} } })
      return route.fulfill({ json: {} })
    })
    await context.routeWebSocket(/\/api\/updates\/ws/, (ws) => ws.close())
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.goto(`${base}/models`)
    await page.getByText('1–6 of 16 available models', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Next catalog page' }).click()
    await page.getByText('7–12 of 16 available models', { exact: true }).waitFor()
    await page.getByRole('combobox', { name: 'Filter available models by provider' }).click()
    await page.getByRole('option', { name: 'beta', exact: true }).click()
    await page.getByText('1–6 of 8 available models', { exact: true }).waitFor()
    assert.equal(await page.locator('.catalog-model-entry').count(), 6)
    const rows = page.locator('.models-table tbody tr')
    assert.match(await rows.first().innerText(), /0\.0%/, 'recorded zero success must be visible')
    const betaCells = await rows.nth(1).locator('td').allInnerTexts()
    assert.equal(betaCells[2], '—', 'no requests must have unknown success')
    await page.getByRole('button', { name: 'Open details for alpha alpha-model' }).focus()
    await page.keyboard.press('Enter')
    await page.getByRole('dialog').waitFor()
    await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Close model details')
    await page.getByRole('tab', { name: 'History', exact: true }).click()
    await page.getByRole('heading', { name: 'Performance history', exact: true }).waitFor()
    await page.keyboard.press('Escape')
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByText('1–3 of 8 available models', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Next catalog page' }).click()
    await page.getByText('4–6 of 8 available models', { exact: true }).waitFor()
    assert.equal(await page.locator('.catalog-model-entry').count(), 3)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
    await page.goto(`${base}/providers`)
    await page.getByRole('button', { name: 'Inspect alpha', exact: true }).waitFor()
    await page.getByRole('combobox', { name: 'Filter provider health' }).click()
    await page.getByRole('option', { name: 'Needs attention', exact: true }).click()
    await page.getByText('1 of 2 providers', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Inspect beta', exact: true }).count(), 0)
    await page.getByRole('button', { name: 'Inspect alpha', exact: true }).click()
    await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Close provider details')
    await page.getByRole('button', { name: 'Performance history', exact: true }).click()
    await page.getByText('Latency', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
    assert.deepEqual(errors, [])
    console.error(
      '[ui-check] PASS catalog: desktop/mobile catalog pagination and provider filtering; zero vs unknown success; keyboard model inspection; model history tab; provider health filter/history disclosure; no page errors or mobile overflow',
    )
  } finally {
    await context.close()
  }
}
