import assert from 'node:assert/strict';

// Keep one real browser-side socket open, while all API data and update events
// are fixtures. This never connects a test to the service's update stream.
export async function checkLiveRegressions(browser, base, overview) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  try {
    // Track the routed app connection directly: Playwright replaces WebSocket
    // with its own init script, whose order relative to other init scripts is
    // unspecified. Wrapping window.WebSocket can miss app sockets entirely.
    const sockets = new Set();
    let connectionCount = 0;
    let activity = null;
    let historyUnavailable = false;
    let detailStatus = 503;
    await context.routeWebSocket(/\/api\/updates\/ws/, (ws) => {
      connectionCount++;
      sockets.add(ws);
      ws.onClose(() => sockets.delete(ws));
    });
    await context.route(/\/(api\/|stats(?:\?|$))/, (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/overview') return route.fulfill({ json: overview });
      if (path === '/stats') return route.fulfill({ json: { models: null } });
      if (path === '/api/requests') return route.fulfill({ json: { requests: null } });
      if (path === '/api/stats/errors') return route.fulfill(historyUnavailable
        ? { status: 503, json: { error: 'shared request history unavailable' } }
        : { json: { errors: activity } });
      if (path === '/api/requests/shared-feed-test') return route.fulfill(detailStatus === 200
        ? { json: { id: 'shared-feed-test', at: activity[0].at, backend: 'example', model: 'shared-model', status: '429' } }
        : { status: detailStatus, json: { error: 'shared request history unavailable' } });
      if (path === '/api/zcode/usage') return route.fulfill({ json: { plans: null } });
      if (path.startsWith('/api/stats')) return route.fulfill({ json: { models: null, series: null } });
      return route.fulfill({ status: 404, json: { error: 'Unexpected fixture request' } });
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${base}/`);
    await page.getByRole('heading', { level: 1, name: 'Overview', exact: true }).waitFor();
    await page.getByText('Live updates', { exact: true }).waitFor();
    assert.equal(sockets.size, 1, 'header and Overview share one open live-update connection');
    const initialConnectionCount = connectionCount;
    for (const name of ['Providers', 'Models', 'Setup', 'Overview']) {
      await page.getByRole('navigation').getByRole('link', { name, exact: true }).click();
      await page.getByRole('heading', { level: 1, name, exact: true }).waitFor();
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForFunction(() => Boolean(document.querySelector('.bottom-navigation')));
    assert.equal(connectionCount, initialConnectionCount,
      'navigation and mobile layout must retain the existing connection');
    assert.equal(sockets.size, 1,
      'header and Overview share one open live-update connection');
    const updated = page.waitForResponse((response) => new URL(response.url()).pathname === '/stats');
    sockets.values().next().value.send(JSON.stringify({ type: 'stats-updated' }));
    await updated;
    assert.deepEqual(errors, [], 'live refresh and null series must not crash the page');
    console.error('[ui-check] PASS one live connection across all routes and mobile layout, event refresh, null history');

    activity = [{ at: new Date().toISOString(), backend: 'example', model: 'shared-model',
      status: '429', message: 'Fixture failure', request_id: 'shared-feed-test' }];
    sockets.values().next().value.send(JSON.stringify({ type: 'stats-updated' }));
    const inspect = page.getByRole('button', { name: 'Inspect request shared-feed-test', exact: true });
    await inspect.waitFor();
    historyUnavailable = true;
    sockets.values().next().value.send(JSON.stringify({ type: 'stats-updated' }));
    await page.getByText("Couldn't refresh upstream errors", { exact: true }).waitFor();
    assert.equal(await inspect.count(), 1, 'shared storage failure must retain the last successful error feed');
    assert.equal(await page.getByText('No recent upstream errors', { exact: true }).count(), 0);
    await inspect.click();
    const dialog = page.getByRole('dialog');
    await dialog.getByText("Couldn't load request details", { exact: true }).waitFor();
    assert.equal(await dialog.getByText('This request is no longer available in the retained history.', { exact: true }).count(), 0,
      'shared storage failure must not claim that the request expired');
    detailStatus = 200;
    await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
    await dialog.getByText('example / shared-model', { exact: true }).waitFor();
    await page.getByRole('button', { name: 'Close request inspection', exact: true }).click();
    detailStatus = 404;
    // A fresh page ensures the successful detail is not already cached.
    historyUnavailable = false;
    await page.reload();
    await inspect.click();
    await dialog.getByText('This request is no longer available in the retained history.', { exact: true }).waitFor();
    assert.deepEqual(errors, [], 'shared activity outage and inspection recovery must not crash the page');
    console.error('[ui-check] PASS shared activity outage retains failures; inspection distinguishes temporary outage from expiry and retries');
  } finally {
    await context.close();
  }
}
