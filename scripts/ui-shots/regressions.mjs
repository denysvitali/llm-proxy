import assert from 'node:assert/strict';
import path from 'node:path';
import { checkSetupRegressions } from './setup-regressions.mjs';
import { checkCatalogRegressions } from './catalog-regressions.mjs';

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
export async function checkRegressions(browser, base, outDir) {
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
        if (path === '/api/overview') return route.fulfill({ json: brokenOverview ? { ...overview, backends: [null] } : overview });
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
      await page.getByLabel('Search providers', { exact: true }).fill('zcode');
      await visible(page.getByText(/1 of 2 providers/));
      assert.equal(await page.getByRole('button', { name: 'Inspect example', exact: true }).count(), 0);
      await page.getByLabel('Search providers', { exact: true }).fill('does-not-exist');
      await visible(page.getByText('No matching providers', { exact: true }));
      await page.getByLabel('Search providers', { exact: true }).fill('');
      if (name === 'null') {
        await page.getByRole('combobox', { name: 'Filter provider health' }).click();
        await page.getByRole('option', { name: 'Needs attention', exact: true }).click();
        await visible(page.getByText('No matching providers', { exact: true }));
        await page.getByRole('combobox', { name: 'Filter provider health' }).click();
        await page.getByRole('option', { name: 'All providers', exact: true }).click();
        await visible(page.getByText(/2 of 2 providers/));
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

        // Catalog entries remain visible even when no model has traffic.
        await page.goto(`${base}/models`);
        await visible(page.getByText('zcode/example-model', { exact: true }));
        await visible(page.getByText('No model traffic yet', { exact: true }));
        assert.deepEqual(errors, []);

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
        await visible(page.getByRole('heading', { name: 'Configure your client' }));
        for (const width of [320, 768, 769, 1024]) {
          await page.setViewportSize({ width, height: 900 });
          await page.waitForFunction((mobile) => Boolean(document.querySelector('.bottom-navigation')) === mobile, width <= 768);
          await page.waitForFunction((mobile) => {
            const left = mobile ? 0 : document.querySelector('.app-sidebar').getBoundingClientRect().right;
            return Math.abs(document.querySelector('.page-container').getBoundingClientRect().left - left) < 0.1;
          }, width <= 768);
          const geometry = await page.evaluate(() => ({
            left: document.querySelector('.page-container').getBoundingClientRect().left,
            expectedLeft: document.querySelector('.app-sidebar')?.getBoundingClientRect().right ?? 0,
            overflow: document.documentElement.scrollWidth > innerWidth + 1,
          }));
          assert.equal(geometry.left, geometry.expectedLeft, `shell offset at ${width}px`);
          assert.equal(geometry.overflow, false, `no horizontal overflow at ${width}px`);
        }

      }
      console.error(`[ui-check] PASS ${name} plans: navigation, reload, search, empty feeds${name === 'null' ? ', failed API and error recovery' : ''}`);
    } finally {
      await context.close();
    }
  }
  await checkSetupRegressions(browser, base, overview, series);
  await checkCatalogRegressions(browser, base);
  return checkMiniMaxRegressions(browser, base, outDir);
}

const resetAt = Date.UTC(2030, 0, 2, 12);
const checkinPanel = (claimed = false) => ({ scene: 1, days: [
  { day_no: 1, points: 50, status: 3, is_today: false },
  { day_no: 2, points: 150, bonus_points: 50, status: claimed ? 3 : 2, is_today: true },
  { day_no: 3, points: 100, status: 1, is_today: false },
  { day_no: 4, points: 100, status: 4, is_today: false },
  { day_no: 5, points: 100, status: 1, is_today: false },
  { day_no: 6, points: 100, status: 1, is_today: false },
  { day_no: 7, points: 250, status: 1, is_today: false },
] });
const miniMaxAccount = {
  has_token_plan: true, tier: 'Regression plan', expires_at_ms: resetAt,
  credit_balance: '1250.5', quota_state: 'available',
  quota: {
    five_hour: { remaining_percent: 75, reset_at_ms: resetAt, unlimited: false },
    weekly: { unlimited: true },
    video: { remaining_count: 0, total_count: 10, unlimited: false },
  },
};

// All reads and writes are intercepted, including the explicit claim. Never let
// these synthetic account fixtures reach the service supplied by --base-url.
export async function checkMiniMaxRegressions(browser, base, outDir) {
  const written = [];
  const cases = [
    { name: 'desktop-light', colorScheme: 'light', claim: 1, screenshot: true },
    { name: 'desktop-dark', colorScheme: 'dark', screenshot: true },
    { name: 'mobile-dark', mobile: true, colorScheme: 'dark', screenshot: true },
    { name: 'mobile-light', mobile: true, colorScheme: 'light', screenshot: true },
    { name: 'already-claimed', claimed: true },
    { name: 'claim-race', claim: 2 },
    { name: 'claim-failure', claim: 'failure' },
    { name: 'zero-null-days', balance: '0', days: null, mobile: true },
    { name: 'unknown-missing-days', balance: undefined, days: undefined },
    { name: 'empty-days', days: [] },
    { name: 'unknown-quota', unknownQuota: true },
    { name: 'account-error', accountError: 'Account service temporarily unavailable' },
    { name: 'checkin-error', checkinError: 'Check-in service temporarily unavailable' },
    { name: 'unavailable', unavailable: true },
  ];
  for (const fixture of cases) {
    const context = await browser.newContext({
      viewport: fixture.mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 },
      isMobile: fixture.mobile ?? false, hasTouch: fixture.mobile ?? false,
      colorScheme: fixture.colorScheme ?? 'light', timezoneId: 'UTC',
    });
    let finishClaim;
    try {
      let claims = 0;
      let reads = 0;
      let claimed = fixture.claimed ?? false;
      const pendingClaim = new Promise((resolve) => { finishClaim = resolve; });
      let receiveClaim;
      const interceptedClaim = new Promise((resolve) => { receiveClaim = resolve; });
      await context.route(/\/(api\/|stats(?:\?|$))/, async (route) => {
        const request = route.request();
        const pathname = new URL(request.url()).pathname;
        if (pathname === '/api/minimax-code/checkin' && request.method() === 'POST') {
          claims++;
          receiveClaim();
          await pendingClaim;
          if (fixture.claim === 'failure') return route.fulfill({ status: 503, json: { error: 'Check-in temporarily unavailable' } });
          claimed = true;
          return route.fulfill({ json: { claim_result: fixture.claim ?? 1,
            day_no: 2, points: 150, expire_at_ms: resetAt, panel: checkinPanel(true) } });
        }
        assert.equal(request.method(), 'GET', `unexpected ${request.method()} ${pathname}`);
        if (pathname === '/api/minimax-code/usage') {
          reads++;
          if (fixture.unavailable) return route.fulfill({ status: 503, json: { error: 'MiniMax temporarily unavailable' } });
          const panel = checkinPanel(claimed);
          if (Object.hasOwn(fixture, 'days')) panel.days = fixture.days;
          const account = { ...miniMaxAccount };
          if (Object.hasOwn(fixture, 'balance')) account.credit_balance = fixture.balance;
          if (fixture.unknownQuota) account.quota = {
            five_hour: {}, weekly: { unlimited: false }, video: { remaining_count: null, unlimited: false },
          };
          return route.fulfill({ json: {
            account: fixture.accountError ? undefined : account,
            checkin: fixture.checkinError ? undefined : panel,
            accountError: fixture.accountError, checkinError: fixture.checkinError,
            fetchedAt: new Date().toISOString(),
          } });
        }
        if (pathname === '/api/overview') return route.fulfill({ json: {
          ...overview, backends: [backend('minimax-code')],
          zcodeUsage: { configured: false, available: false },
          minimaxUsage: { configured: true, available: true },
        } });
        if (pathname === '/stats') return route.fulfill({ json: { models: [] } });
        if (pathname === '/api/stats/errors') return route.fulfill({ json: { errors: [] } });
        if (pathname === '/api/requests') return route.fulfill({ json: { requests: [] } });
        if (pathname.startsWith('/api/stats')) return route.fulfill({ json: { models: [], series } });
        return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' });
      });
      await context.routeWebSocket(/\/api\/updates\/ws/, (ws) => ws.close());
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(`${base}/providers`);
      await visible(page.getByRole('heading', { level: 1, name: 'Providers' }));
      if (fixture.screenshot) {
        await page.getByRole('button', { name: 'Inspect minimax-code', exact: true }).click();
        const drawer = page.getByRole('dialog');
        const drawerCard = drawer.getByRole('region', { name: 'MiniMax Code account', exact: true });
        await visible(drawerCard.getByRole('heading', { name: 'MiniMax Code', exact: true }));
        await visible(drawerCard.getByRole('button', { name: 'Check in', exact: true }));
        await visible(drawerCard.getByText('Day 7', { exact: true }));
        assert.equal(claims, 0, 'opening the provider drawer must not claim credits');
        if (outDir) {
          await drawerCard.scrollIntoViewIfNeeded();
          await page.evaluate(() => document.fonts.ready);
          const file = path.join(outDir, `minimax-drawer__${fixture.name}.png`);
          await page.screenshot({ path: file, animations: 'disabled' });
          written.push(file);
        }
        await page.keyboard.press('Escape');
        await drawer.waitFor({ state: 'hidden' });
      }
      await page.getByRole('navigation').getByRole('link', { name: 'Overview', exact: true }).click();
      await visible(page.getByRole('heading', { level: 1, name: 'Overview' }));
      await page.getByRole('button', { name: /Subscription usage/ }).click();
      const card = page.getByRole('region', { name: 'MiniMax Code account', exact: true });
      await visible(card);
      await visible(card.getByRole('heading', { name: 'MiniMax Code', exact: true }));

      if (fixture.unavailable) {
        await visible(card.getByText('Usage unavailable', { exact: true }));
      } else {
        if (fixture.accountError) {
          await visible(card.getByText('Account unavailable', { exact: true }));
          await visible(card.getByText(fixture.accountError, { exact: true }));
          await visible(card.getByRole('button', { name: 'Check in', exact: true }));
        } else {
          const credit = card.getByText('Credit balance', { exact: true }).locator('..');
          await visible(credit.getByText(Object.hasOwn(fixture, 'balance')
            ? fixture.balance === undefined ? 'Not reported' : '0'
            : '1250.5', { exact: true }));
          if (fixture.unknownQuota) {
            await visible(card.getByText('Not reported', { exact: true }).first());
            assert.equal(await card.getByText(/% remaining/).count(), 0, 'missing quota counters must not become zero');
          } else {
            await visible(card.getByText('75% remaining', { exact: true }));
            await visible(card.getByText('Unlimited', { exact: true }));
            await visible(card.getByText('0 of 10 remaining', { exact: true }));
            await visible(card.getByText(/Resets /).first());
            await visible(card.locator(`time[datetime="${new Date(resetAt).toISOString()}"]`));
          }
        }
        if (fixture.checkinError) {
          await visible(card.getByText('Check-in unavailable', { exact: true }));
          await visible(card.getByText(fixture.checkinError, { exact: true }));
        } else if (Object.hasOwn(fixture, 'days')) {
          assert.equal(await card.getByRole('button', { name: 'Check in', exact: true }).isEnabled(), false);
        } else if (fixture.claimed) {
          assert.equal(await card.getByRole('button', { name: 'Checked in today', exact: true }).isEnabled(), false);
        } else {
          await visible(card.getByText('Includes 50 bonus', { exact: true }));
          await visible(card.getByText('150 credits', { exact: true }));
          assert.equal(await card.getByText('200 credits', { exact: true }).count(), 0, 'bonus is already included in reward');
          assert.equal(await card.getByRole('button', { name: 'Check in', exact: true }).isEnabled(), true);
        }
      }
      assert.ok(reads > 0, `${fixture.name}: usage fetched`);
      assert.equal(claims, 0, `${fixture.name}: loading usage must never claim credits`);

      if (fixture.screenshot && outDir) {
        await card.scrollIntoViewIfNeeded();
        await page.evaluate(() => document.fonts.ready);
        if (fixture.mobile) {
          const viewportFile = path.join(outDir, `minimax-viewport__${fixture.name}.png`);
          await page.screenshot({ path: viewportFile, animations: 'disabled' });
          written.push(viewportFile);
          // Keep the realistic viewport shot above; use extra height for the
          // complete card so fixed mobile navigation cannot cover its schedule.
          await page.setViewportSize({ width: 390, height: 1400 });
          await card.scrollIntoViewIfNeeded();
        }
        const file = path.join(outDir, `minimax__${fixture.name}.png`);
        await card.screenshot({ path: file, animations: 'disabled' });
        written.push(file);
        if (fixture.mobile) await page.setViewportSize({ width: 390, height: 844 });
      }
      if (fixture.claim) {
        const claimButton = card.getByRole('button', { name: 'Check in', exact: true });
        const submitted = page.waitForRequest((request) => request.url().endsWith('/api/minimax-code/checkin') && request.method() === 'POST');
        await claimButton.click();
        await submitted;
        // The request event can precede the route handler. Wait for the mock
        // to count the request before checking the single-submission invariant.
        await interceptedClaim;
        assert.equal(claims, 1, 'explicit claim sends one POST');
        await visible(claimButton.and(page.locator(':disabled')));
        assert.equal(await claimButton.isEnabled(), false, 'pending claim prevents another submission');
        finishClaim();
        if (fixture.claim === 'failure') {
          await visible(card.getByText('Could not confirm check-in', { exact: true }));
          await page.waitForTimeout(1500);
          assert.equal(claims, 1, 'failed claim must never retry automatically');
        } else {
          await visible(card.getByRole('button', { name: 'Checked in today', exact: true }));
          assert.equal(await card.getByRole('button', { name: 'Checked in today', exact: true }).isEnabled(), false);
          if (fixture.claim === 2) await visible(card.getByText(/already checked in/i).first());
        }
        assert.equal(claims, 1, 'claim and refresh must issue exactly one POST');
        assert.ok(reads > 1, 'claim outcome refreshes account status');
      }
      assert.deepEqual(errors, [], `${fixture.name}: MiniMax state must render without errors`);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${fixture.name}: content must fit`);
      console.error(`[ui-check] PASS MiniMax ${fixture.name}`);
    } finally {
      finishClaim?.();
      await context.close();
    }
  }
  return written;
}
