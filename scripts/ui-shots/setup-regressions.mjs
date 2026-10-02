import assert from 'node:assert/strict';

// Intercept every API used here: checks must never create live inference traffic.
export async function checkSetupRegressions(browser, base, overview, series) {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const healthRequests = [];
  const inferenceRequests = [];
  let healthStatus = 200;
  let networkFailure = false;
  try {
    await context.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async (value) => { window.copiedSnippet = value; } } });
    });
    await context.route(/\/(api\/|stats(?:\?|$))/, (route) => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === '/api/overview') return route.fulfill({ json: overview });
      if (pathname === '/stats') return route.fulfill({ json: { models: [] } });
      if (pathname.startsWith('/api/stats')) return route.fulfill({ json: { models: [], series } });
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' });
    });
    await context.routeWebSocket(/\/api\/updates\/ws/, (ws) => ws.close());
    await context.route('**/healthz', (route) => {
      healthRequests.push(route.request().method());
      return networkFailure ? route.abort('failed') : route.fulfill({ status: healthStatus, json: { status: healthStatus === 200 ? 'ok' : 'unavailable' } });
    });
    await context.route('**/v1/**', (route) => {
      inferenceRequests.push(route.request().url());
      return route.abort('blockedbyclient');
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${base}/setup`);
    await page.getByRole('heading', { name: 'Choose your client' }).waitFor();
    assert.deepEqual(healthRequests, [], 'health check must require an explicit click');
    await page.getByRole('button', { name: 'Copy Claude Code snippet' }).click();
    assert.equal(await page.evaluate(() => window.copiedSnippet), overview.claudeSnippet);
    await page.getByRole('tab', { name: 'Codex CLI', exact: true }).click();
    await page.getByRole('region', { name: 'Codex CLI setup snippet' }).waitFor();
    await page.getByRole('button', { name: 'Copy Codex CLI snippet' }).click();
    assert.equal(await page.evaluate(() => window.copiedSnippet), overview.codexSnippet);
    await page.getByRole('tab', { name: 'HTTP / curl', exact: true }).click();
    const snippet = page.getByRole('region', { name: 'Other / curl setup snippet' });
    assert.match(await snippet.innerText(), /zcode\/example-model/);
    await page.getByRole('switch', { name: 'Wrap Other / curl snippet lines' }).uncheck();
    assert.equal(await snippet.evaluate((node) => getComputedStyle(node).whiteSpace), 'pre');
    await page.getByRole('switch', { name: 'Wrap Other / curl snippet lines' }).check();
    const check = page.getByRole('button', { name: 'Check connection', exact: true });
    await check.click();
    await page.getByText('Gateway is reachable.', { exact: true }).waitFor();
    healthStatus = 503;
    await check.click();
    await page.getByText('Gateway returned HTTP 503.', { exact: true }).waitFor();
    networkFailure = true;
    await check.click();
    await page.getByText('Could not reach the gateway. Check your connection and try again.', { exact: true }).waitFor();
    assert.deepEqual(healthRequests, ['GET', 'GET', 'GET']);
    assert.deepEqual(inferenceRequests, []);

    // Navigation sequences are consumed once and never hijack text entry.
    await page.locator('#main').focus();
    await page.keyboard.press('g');
    await page.keyboard.press('m');
    await page.getByRole('heading', { level: 1, name: 'Models', exact: true }).waitFor();
    await page.keyboard.press('s');
    assert.equal(new URL(page.url()).pathname, '/models', 'a completed sequence must not leak into another key');
    const search = page.getByLabel('Find an available model', { exact: true });
    await search.fill('');
    await search.pressSequentially('gs');
    assert.equal(new URL(page.url()).pathname, '/models', 'typing in search must not navigate');
    await page.locator('#main').focus();
    await page.keyboard.press('Control+g');
    await page.keyboard.press('s');
    assert.equal(new URL(page.url()).pathname, '/models', 'modified keys must not start navigation');
    await page.keyboard.press('?');
    await page.getByRole('dialog', { name: 'Keyboard shortcuts' }).waitFor();
    await page.keyboard.press('g');
    await page.keyboard.press('s');
    assert.equal(new URL(page.url()).pathname, '/models', 'shortcuts must pause inside dialogs');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    await page.locator('#main').focus();
    await page.keyboard.press('g');
    await page.keyboard.press('s');
    await page.getByRole('heading', { level: 1, name: 'Setup', exact: true }).waitFor();
    assert.deepEqual(errors, []);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
    console.error('[ui-check] PASS Setup and shell: client tabs, exact copy, wrapping, explicit health checks, error recovery, no inference, guarded keyboard navigation');
  } finally {
    await context.close();
  }
}
