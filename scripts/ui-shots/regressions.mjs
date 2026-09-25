import assert from 'node:assert/strict';

const backend = (name) => ({ name, enabled: true, host: 'example.invalid', hasKey: true,
  authLabel: 'API key', authConfigured: true, catalogOK: true, models: [`${name}/example-model`] });
const overview = { name: 'llm-proxy', version: 'test', listen: ':8090', authEnabled: true,
  backends: [backend('zcode'), backend('example')], routes: [],
  grokUsage: { configured: false, available: false }, zcodeUsage: { configured: true, available: true },
  hasDefault: false, defaultRoute: {}, exampleModel: 'zcode/example-model',
  claudeSnippet: 'example command', codexSnippet: 'example config' };
const series = Object.fromEntries(['requests', 'success_rate', 'ttft_p50', 'e2e_p50', 'throughput_p50',
  'tokens_in', 'tokens_out', 'tool_calls', 'tool_errors'].map((key) => [key, []]));

async function visible(locator) {
  await locator.waitFor({ state: 'visible', timeout: 10000 });
}

// Every API response is local to the browser. These checks issue no inference,
// sign-ins, or configuration writes, even when --base-url points at a cluster.
export async function checkRegressions(browser, base) {
  for (const [name, plans] of [
    ['null', null], ['missing', undefined], ['empty', []],
    ['populated', [{ plan_id: 'regression-plan', name: 'Regression plan', total_units: 100, used_units: 25 }]],
  ]) {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
    try {
      let brokenOverview = false;
      let unavailableStats = false;
      await context.route(/\/(api\/|stats(?:\?|$))/, async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/overview') return route.fulfill({ json: brokenOverview ? { ...overview, backends: null } : overview });
        if (path === '/stats') return route.fulfill(unavailableStats ? { status: 503, json: { error: 'unavailable' } } : { json: { models: null } });
        if (path === '/api/zcode/usage') return route.fulfill({ json: { plans, fetchedAt: new Date().toISOString() } });
        if (path === '/api/stats/errors') return route.fulfill({ json: { errors: null } });
        if (path === '/api/requests') return route.fulfill({ json: { requests: null } });
        if (path.startsWith('/api/stats')) return route.fulfill({ json: { models: [], series } });
        return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' });
      });
      // Avoid connecting a regression fixture to real live-update events.
      await context.routeWebSocket(/\/api\/updates\/ws/, (ws) => ws.close());
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(`${base}/providers`);
      await visible(page.getByRole('heading', { level: 1, name: 'Providers' }));
      await visible(page.getByRole('button', { name: 'Inspect zcode', exact: true }));
      await page.getByRole('textbox', { name: 'Search providers' }).fill('zcode');
      await visible(page.getByText('Showing 1 of 2 providers', { exact: false }));
      assert.equal(await page.getByRole('button', { name: 'Inspect example', exact: true }).count(), 0);
      await page.getByRole('textbox', { name: 'Search providers' }).fill('does-not-exist');
      await visible(page.getByText('No matching providers', { exact: true }));
      await page.getByRole('textbox', { name: 'Search providers' }).fill('');
      if (name === 'null') {
        await page.getByRole('combobox', { name: 'Filter provider health' }).click();
        await page.getByRole('option', { name: 'Needs attention', exact: true }).click();
        await visible(page.getByText('No matching providers', { exact: true }));
        await page.getByRole('combobox', { name: 'Filter provider health' }).click();
        await page.getByRole('option', { name: 'All providers', exact: true }).click();
        await visible(page.getByText('Showing 2 of 2 providers', { exact: false }));
      }

      await page.getByRole('navigation').getByRole('link', { name: 'Overview', exact: true }).click();
      await visible(page.getByRole('heading', { level: 1, name: 'Overview' }));
      await page.getByRole('button', { name: /Subscription usage/ }).click();
      await visible(page.getByText(name === 'populated' ? 'Regression plan' : 'No plan usage is available for this account.', { exact: true }));
      await visible(page.getByText('No recent upstream errors', { exact: true }));
      assert.deepEqual(errors, [], `${name} plans must not crash on client-side navigation`);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'mobile content must fit');
      await page.reload();
      await visible(page.getByRole('heading', { level: 1, name: 'Overview' }));
      await page.getByRole('button', { name: /Subscription usage/ }).click();
      await visible(page.getByText(name === 'populated' ? 'Regression plan' : 'No plan usage is available for this account.', { exact: true }));
      assert.deepEqual(errors, [], `${name} plans must not crash on direct load`);

      if (name === 'null') {
        // A failed stats endpoint must not become a healthy/no-traffic result.
        unavailableStats = true;
        await page.goto(`${base}/providers`);
        await visible(page.getByText('Request stats unavailable', { exact: true }));
        assert.equal(await page.getByText('no traffic', { exact: true }).count(), 0);
        assert.deepEqual(errors, []);
        unavailableStats = false;

        // Deliberately malformed data verifies the page boundary and recovery.
        brokenOverview = true;
        await page.goto(`${base}/setup`);
        await visible(page.getByText("This page couldn't be displayed", { exact: true }));
        await page.getByRole('navigation').getByRole('link', { name: 'Models', exact: true }).click();
        await visible(page.getByRole('heading', { level: 1, name: 'Models' }));
        await page.goto(`${base}/setup`);
        await visible(page.getByRole('button', { name: 'Reload page', exact: true }));
        brokenOverview = false;
        await page.getByRole('button', { name: 'Reload page', exact: true }).click();
        await visible(page.getByRole('heading', { name: '1. Check your connection' }));
        for (const width of [320, 768, 769, 1024]) {
          await page.setViewportSize({ width, height: 900 });
          await page.waitForFunction((mobile) => Boolean(document.querySelector('.bottom-navigation')) === mobile, width <= 768);
          await page.waitForFunction((left) => Math.abs(document.querySelector('.page-container').getBoundingClientRect().left - left) < 0.1, width <= 768 ? 0 : 216);
          const geometry = await page.evaluate(() => ({
            left: document.querySelector('.page-container').getBoundingClientRect().left,
            overflow: document.documentElement.scrollWidth > innerWidth + 1,
          }));
          assert.equal(geometry.left, width <= 768 ? 0 : 216, `shell offset at ${width}px`);
          assert.equal(geometry.overflow, false, `no horizontal overflow at ${width}px`);
        }

      }
      console.error(`[ui-check] PASS ${name} plans: navigation, reload, search, empty feeds${name === 'null' ? ', failed API and error recovery' : ''}`);
    } finally {
      await context.close();
    }
  }
}
