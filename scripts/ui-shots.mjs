#!/usr/bin/env node
// ui-shots.mjs -- re-runnable Playwright screenshot harness for the llm-proxy
// dashboard SPA.
//
// Captures every route in web/src/App.tsx across three viewports/schemes, plus
// the scroll-to-footer state and both page Drawers, against the real Go server
// (which serves the SPA same-origin, so routing/proxying is faithful).
//
// Usage:
//   node scripts/ui-shots.mjs [--out DIR] [--only id,id] [--skip-build] [--list]
//
//   --out DIR       output directory (default: /tmp/ui-shots/current)
//   --only a,b      capture only the listed shot ids (see --list)
//   --skip-build    reuse the existing web/dist + webdist instead of rebuilding
//   --list          print the available shot ids and exit
//   --base-url URL  read-only capture from an existing server (no build or seed)
//   --full-page     capture complete page height
//   --regression    run mocked navigation and empty-data regression checks
//
// stdout = the absolute path of every PNG written, one per line (pipeable).
// stderr = progress + warnings. Non-zero exit means the run failed.
//
// Re-run this after editing anything under web/src/.

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..');
const WEB = path.join(ROOT, 'web');
const WEB_DIST = path.join(WEB, 'dist');
const WEBDIST = path.join(ROOT, 'internal', 'server', 'web', 'webdist');
const MOCK_UPSTREAM = path.join(ROOT, 'scripts', 'e2e', 'mock_upstream.py');

// --- fixed, proven-good toolchain locations on this box ---------------------
// Defaults use the installed workspace toolchain; environment variables can
// select another Playwright module or compatible Chromium binary.
const PLAYWRIGHT_CJS = process.env.PLAYWRIGHT_MODULE || '/home/workspace/.npm/_npx/e41f203b7505f1fb/node_modules/playwright/index.js';
const BROWSER_BIN = process.env.CHROMIUM_EXECUTABLE ||
  '/home/workspace/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell';
const SHIM_DIR = '/tmp/pwchrome-libs';
const FONTCONFIG = '/tmp/fonts.conf';
const FONT_DIR = '/home/workspace/firmware/ZEN3-2026.8.6/rootfs/usr/share/fonts/noto';

const log = (m) => process.stderr.write(m + '\n');
const warn = (m) => process.stderr.write(`WARNING: ${m}\n`);

// ---------------------------------------------------------------- args ------
function parseArgs(argv) {
  const opts = { out: '/tmp/ui-shots/current', only: null, skipBuild: false, list: false, baseURL: null, fullPage: false, regression: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--out') opts.out = path.resolve(argv[++i]);
    else if (a.startsWith('--out=')) opts.out = path.resolve(a.slice(6));
    else if (a === '--only') opts.only = argv[++i].split(',').map((s) => s.trim()).filter(Boolean);
    else if (a.startsWith('--only=')) opts.only = a.slice(7).split(',').map((s) => s.trim()).filter(Boolean);
    else if (a === '--base-url') opts.baseURL = new URL(argv[++i]).href.replace(/\/$/, '');
    else if (a === '--full-page') opts.fullPage = true;
    else if (a === '--regression') opts.regression = true;
    else if (a === '--skip-build') opts.skipBuild = true;
    else if (a === '--list') opts.list = true;
    else die(`unknown argument: ${a}  (try --help)`);
  }
  return opts;
}

// ------------------------------------------------------------- shot plan ----
const ROUTES = [
  { id: 'home', slug: 'home', route: '/' },
  { id: 'models', slug: 'models', route: '/models' },
  { id: 'providers', slug: 'providers', route: '/providers' },
  { id: 'setup', slug: 'setup', route: '/setup' },
];

const VIEWS = {
  'desktop-light': { colorScheme: 'light', viewport: { width: 1440, height: 900 } },
  'desktop-dark': { colorScheme: 'dark', viewport: { width: 1440, height: 900 } },
  // Both schemes get a narrow touch viewport.
  'mobile-light': {
    colorScheme: 'light',
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2, isMobile: true, hasTouch: true,
  },
  'mobile-dark': {
    colorScheme: 'dark',
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  },
};

function buildShots() {
  const shots = [];
  for (const r of ROUTES) {
    shots.push({ id: `${r.id}-light`, route: r, view: 'desktop-light' });
    shots.push({ id: `${r.id}-dark`, route: r, view: 'desktop-dark' });
    shots.push({ id: `${r.id}-mobile`, route: r, view: 'mobile-dark' });
    shots.push({ id: `${r.id}-mobile-light`, route: r, view: 'mobile-light' });
  }
  shots.push({ id: 'home-footer', route: ROUTES[0], view: 'desktop-dark', action: 'scrollBottom' });
  shots.push({
    id: 'models-drawer',
    route: ROUTES[1],
    view: 'desktop-dark',
    action: 'drawer',
    trigger: '[aria-label^="Open details for"]',
  });
  shots.push({
    id: 'providers-drawer',
    route: ROUTES[2],
    view: 'desktop-dark',
    action: 'drawer',
    trigger: '[aria-label^="Inspect "]',
  });
  for (const shot of shots.filter((item) => item.action === 'drawer')) {
    shots.push({ ...shot, id: `${shot.id}-mobile`, view: 'mobile-dark' });
  }
  return shots;
}

function shotFile(shot) {
  const suffix = shot.action === 'drawer' ? '__drawer' : shot.action === 'scrollBottom' ? '__footer' : '';
  return `${shot.route.slug}__${shot.view}${suffix}.png`;
}

// --------------------------------------------------------------- helpers ----
function die(msg, tail) {
  process.stderr.write(`\nERROR: ${msg}\n`);
  if (tail) process.stderr.write(tail);
  process.exit(1);
}

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

const sleep = (ms) => new Promise((r) => { const t = setTimeout(r, ms); t.unref?.(); });

async function waitForHTTP(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url);
      if (r.ok) return true;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  return false;
}

function tailFile(file, lines = 40) {
  try {
    return fs.readFileSync(file, 'utf8').trimEnd().split('\n').slice(-lines).join('\n');
  } catch {
    return '(no log output)';
  }
}

// ------------------------------------------------ browser shim lib build -----
// chrome-headless-shell will not start on this box: it is missing glib/nss/X11
// and friends, and there is no apt/sudo to install them. We build a MINIMAL
// self-contained shim dir from firmware rootfs trees and point the *browser*
// process at it via LD_LIBRARY_PATH.
//
// TRAP (a): do NOT dump every lib from the vm-lab farms (~1431 files) in here.
// Mixed-ABI shadows segfault chrome with exit 139. Only copy what ldd reports
// as genuinely missing, iteratively, until ldd is clean.
const LIB_ROOTS = [
  '/home/workspace/git/chromium-analysis/firmware-libs/usr/lib',
  '/home/workspace/firmware/ZEN3-2026.8.6/rootfs/usr/lib',
  '/home/workspace/git/chromium-analysis/zen3-rootfs/usr/lib',
  '/home/workspace/git/fw-analyzer/firmware-analysis/zen3-2026.8.6/rootfs/usr/lib',
  // rootfs/lib (not usr/lib) holds libmount.so.1 with the MOUNT_2_40 symbol.
  // The system copy tops out at MOUNT_2_39, which is a *version* error, not a
  // missing file, so ldd never reports it -- it has to be seeded deliberately.
  '/home/workspace/firmware/ZEN3-2026.8.6/rootfs/lib',
  '/home/workspace/git/chromium-analysis/zen3-rootfs/lib',
  '/home/workspace/git/fw-analyzer/firmware-analysis/zen3-2026.8.6/rootfs/lib',
  '/home/workspace/vm-lab/gps-libs',
  '/home/workspace/vm-lab/qt-sonames',
  '/home/workspace/vm-lab/zen3-liblinks',
];

function missingSonames() {
  // NOTE: deliberately no `set -o pipefail` style short-circuit here. We must
  // read ldd's full output, because grep -q / early exit SIGPIPEs the producer
  // and makes the whole thing look like a failure.
  const r = spawnSync('ldd', [BROWSER_BIN], {
    env: { ...process.env, LD_LIBRARY_PATH: SHIM_DIR },
    encoding: 'utf8',
  });
  const missing = new Set();
  for (const line of (r.stdout || '').split('\n')) {
    const t = line.trim().split(/\s+/);
    if (t[1] === '=>' && t[2] === 'not') missing.add(t[0]);
  }
  return [...missing].sort();
}

function resolveLib(soname) {
  for (const root of LIB_ROOTS) {
    const p = path.join(root, soname);
    // statSync follows symlinks and returns undefined (thanks to
    // throwIfNoEntry) for the dangling entries in the vm-lab symlink farms.
    const st = fs.statSync(p, { throwIfNoEntry: false });
    if (st?.isFile()) return p;
  }
  return null;
}

function ensureBrowserLibs() {
  if (!fs.existsSync(BROWSER_BIN)) {
    die(
      `browser binary not found: ${BROWSER_BIN}\n` +
        '  It lives in the playwright cache, not the repo. See scripts/ui-shots/README.md.',
    );
  }
  fs.mkdirSync(SHIM_DIR, { recursive: true });
  if (missingSonames().length === 0) {
    log(`  shim libs: already ldd-clean (${fs.readdirSync(SHIM_DIR).length} in ${SHIM_DIR})`);
    return;
  }
  log(`  shim libs: building minimal set in ${SHIM_DIR} ...`);

  // Seed libmount first -- see the comment on LIB_ROOTS.
  for (const r of LIB_ROOTS.filter((p) => p.endsWith('/lib'))) {
    const p = path.join(r, 'libmount.so.1');
    if (!fs.existsSync(p)) continue;
    // Confirm it really has MOUNT_2_40 before shipping it.
    const out = spawnSync('strings', ['-a', p], { encoding: 'utf8' });
    if ((out.stdout || '').includes('MOUNT_2_40')) {
      fs.copyFileSync(p, path.join(SHIM_DIR, 'libmount.so.1'));
      log('  shim libs: seeded libmount.so.1 (MOUNT_2_40)');
      break;
    }
  }

  for (let round = 1; round <= 12; round++) {
    const missing = missingSonames();
    if (missing.length === 0) {
      log(`  shim libs: ldd clean after ${round - 1} copy round(s)`);
      break;
    }
    log(`  shim libs: round ${round} -- ${missing.length} missing`);
    let unresolved = 0;
    for (const soname of missing) {
      const src = resolveLib(soname);
      if (!src) {
        warn(`shim lib ${soname} not found in any firmware rootfs tree`);
        unresolved++;
        continue;
      }
      fs.copyFileSync(src, path.join(SHIM_DIR, soname));
    }
    if (unresolved > 0) {
      die(
        `${unresolved} soname(s) could not be resolved -- chrome cannot start.\n` +
          '  Re-check LIB_ROOTS in scripts/ui-shots.mjs against the current paths on disk.',
      );
    }
  }
  const still = missingSonames();
  if (still.length) die(`shim libs incomplete, still missing: ${still.join(', ')}`);
}

function ensureFontConfig() {
  if (!fs.existsSync(FONT_DIR)) {
    die(
      `font directory not found: ${FONT_DIR}\n` +
        '  Point the <dir> element in the FONTCONFIG template at a real Noto dir.',
    );
  }
  fs.mkdirSync('/tmp/fontconfig-cache', { recursive: true });
  fs.writeFileSync(
    FONTCONFIG,
    `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
  <dir>${FONT_DIR}</dir>
  <cachedir>/tmp/fontconfig-cache</cachedir>
  <cachedir prefix="xdg">fontconfig</cachedir>
  <match target="font">
    <edit name="antialias" mode="assign"><bool>true</bool></edit>
    <edit name="hinting" mode="assign"><bool>false</bool></edit>
    <edit name="hintstyle" mode="assign"><const>hintslight</const></edit>
    <edit name="rgba" mode="assign"><const>none</const></edit>
  </match>
</fontconfig>
`,
  );
  log(`  fontconfig: ${FONTCONFIG}`);
}

// ------------------------------------------------------------------ build ---
function buildSpa() {
  const vite = path.join(WEB, 'node_modules', '.bin', 'vite');
  if (!fs.existsSync(vite)) die(`vite not installed at ${vite} -- web/node_modules is missing`);

  const r = spawnSync('npm', ['run', 'build'], { cwd: WEB, encoding: 'utf8' });
  if (r.status !== 0) {
    die('vite build failed:\n' + ((r.stdout || '') + (r.stderr || '')).trimEnd().split('\n').slice(-40).join('\n'));
  }
  if (!fs.existsSync(path.join(WEB_DIST, 'index.html'))) die('vite build produced no web/dist/index.html');
}

function copyDistToWebdist() {
  // webdist/ is go:embed-ed, so the Go binary must be rebuilt after this.
  fs.rmSync(WEBDIST, { recursive: true, force: true });
  fs.cpSync(WEB_DIST, WEBDIST, { recursive: true });
}

// ----------------------------------------------------------------- server ---
const owned = [];
function cleanup() {
  // Only ever kill PIDs we started ourselves. SIGTERM (not the default
  // SIGKILL) so the proxy gets to log its "shutting down" line.
  for (const child of owned) {
    try {
      child.kill('SIGTERM');
    } catch {
      /* already gone */
    }
  }
}
process.on('exit', cleanup);
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => {
    cleanup();
    process.exit(130);
  });
}

function start(label, cmd, args, opts) {
  const out = fs.openSync(opts.log, 'w');
  const child = spawn(cmd, args, { stdio: ['ignore', out, out], ...opts.spawnOpts });
  child.on('error', (e) => die(`failed to start ${label}: ${e.message}`));
  owned.push(child);
  return child;
}

function writeConfig(workDir, proxyPort, mockPort) {
  const cfg = `server:
  listen: 127.0.0.1:${proxyPort}
log_level: info
backends:
  - type: opencode
    base_url: http://127.0.0.1:${mockPort}/v1
    api_key: local-mock-key
    default_model: stealth-mock
default_route:
  backend: opencode
# CRITICAL for deterministic screenshots. Without this, internal/config/loader.go
# defaults stats.persist_file to ~/.local/state/llm-proxy/stats.json -- a file
# shared by every llm-proxy run on this box -- and server.go loads it at startup.
# The dashboard then renders whatever models other agents happened to request.
# Empty disables persistence entirely.
stats:
  persist_file: ""
`;
  const p = path.join(workDir, 'config.yaml');
  fs.writeFileSync(p, cfg);
  return p;
}

async function seedTraffic(base, total = 8) {
  // The mock is started with --fail-first 2 --fail-status 500, so the first
  // two requests fail. That gives the Overview/Models pages a real, partial
  // success-rate bar to render instead of a vacuous 100%.
  for (let i = 0; i < total; i++) {
    try {
      await fetch(`${base}/v1/chat/completions`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          model: 'stealth-mock',
          messages: [{ role: 'user', content: 'screenshot seed' }],
          max_tokens: 8,
        }),
      });
    } catch {
      /* the proxy may not answer 500s in the way we expect; stats still fill */
    }
  }
}

// ---------------------------------------------------------------- capture ---
async function capture(outDir, base, shots, opts) {
  const pw = (await import(PLAYWRIGHT_CJS)).default;
  const { chromium } = pw;

  // TRAP (b): LD_LIBRARY_PATH must be scoped to the BROWSER process only.
  // Exporting it in the node process env segfaults node itself (exit 139) --
  // it is a node that was itself linked against a shadowed lib.
  const browser = await chromium.launch({
    executablePath: BROWSER_BIN,
    args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
    env: {
      ...process.env,
      LD_LIBRARY_PATH: SHIM_DIR,
      FONTCONFIG_FILE: FONTCONFIG,
      TMPDIR: '/tmp',
    },
  });

  // One context per (viewport, scheme) pair, reused for every shot within it.
  // colorScheme is a context-level option, so this is the reliable way to
  // force Mantine's auto/light/dark resolution.
  const contexts = {};
  for (const [name, v] of Object.entries(VIEWS)) {
    const { viewport, ...rest } = v;
    contexts[name] = await browser.newContext({ viewport, ...rest });
  }

  const written = [];
  const consoleErrors = [];
  try {
    for (const shot of shots) {
      const ctx = contexts[shot.view];
      const page = await ctx.newPage();
      const onConsole = (m) => {
        if (m.type() === 'error') consoleErrors.push(`${shot.id}: ${m.text()}`);
      };
      page.on('console', onConsole);
      const pageErrors = [];
      page.on('pageerror', (e) => { pageErrors.push(e.message); consoleErrors.push(`${shot.id}: pageerror: ${e.message}`); });

      const url = base + shot.route.route;
      try {
        await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });
      } catch (e) {
        warn(`goto ${url} did not reach networkidle (${e.message.split('\n')[0]}); shooting anyway`);
      }
      // networkidle alone races the react-query fetches; give the SPA a beat
      // to paint real data instead of the loading skeletons.
      await page.getByRole('heading', { level: 1 }).waitFor({ timeout: 15000 });
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(700);

      if (shot.action === 'scrollBottom') {
        await page.evaluate(() => {
          window.scrollTo(0, document.documentElement.scrollHeight);
          // Mantine ScrollArea (if the page uses one) scrolls its own viewport.
          for (const el of document.querySelectorAll('.mantine-ScrollArea-viewport')) {
            el.scrollTop = el.scrollHeight;
          }
        });
        await page.waitForTimeout(700);
      }

      if (shot.action === 'drawer') {
        const trigger = page.locator(shot.trigger).first();
        const n = await page.locator(shot.trigger).count();
        if (n === 0) {
          throw new Error(`no element matches ${shot.trigger} for ${shot.id}`);
        } else {
          await trigger.click();
          // Mantine Drawer mounts with role=dialog; wait for it, but do not
          // allow a short transition before capturing.
          await page
            .locator('[role="dialog"]')
            .first()
            .waitFor({ state: 'visible', timeout: 5000 });
          await page.waitForTimeout(800);
        }
      }

      const file = path.join(outDir, shotFile(shot));
      await page.screenshot({ path: file, fullPage: opts.fullPage, animations: 'disabled' });
      if (pageErrors.length) throw new Error(`${shot.id}: ${pageErrors.join('; ')}`);
      if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)) {
        throw new Error(`${shot.id}: page overflows horizontally`);
      }
      written.push(file);
      log(`  shot ${shot.id} -> ${file}`);

      page.off('console', onConsole);
      await page.close();
    }
    if (opts.regression) {
      const { checkRegressions } = await import('./ui-shots/regressions.mjs');
      await checkRegressions(browser, base);
    }
  } finally {
    for (const c of Object.values(contexts)) await c.close().catch(() => {});
    await browser.close().catch(() => {});
  }
  return { written, consoleErrors };
}

// ------------------------------------------------------------------ main ----
async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const allShots = buildShots();

  if (opts.list) {
    for (const s of allShots) process.stdout.write(`${s.id}\t${shotFile(s)}\n`);
    return;
  }
  if (opts.only) {
    const unknown = opts.only.filter((id) => !allShots.some((s) => s.id === id));
    if (unknown.length) die(`unknown --only id(s): ${unknown.join(', ')} (use --list)`);
  }
  const shots = opts.only ? allShots.filter((s) => opts.only.includes(s.id)) : allShots;

  fs.mkdirSync(opts.out, { recursive: true });
  log(`[ui-shots] out = ${opts.out}`);
  log(`[ui-shots] shots = ${shots.length}${opts.only ? ` (filtered: ${opts.only.join(',')})` : ''}`);

  ensureFontConfig();
  ensureBrowserLibs();

  // Existing servers are reference-only: never build, start mocks, or seed traffic.
  if (opts.baseURL) {
    const { written, consoleErrors } = await capture(opts.out, opts.baseURL, shots, opts);
    for (const file of written) process.stdout.write(file + '\n');
    for (const error of consoleErrors) warn(error);
    log(`[ui-shots] captured ${written.length} reference(s) from ${opts.baseURL}`);
    return;
  }

  if (opts.skipBuild) {
    warn('--skip-build: reusing the existing webdist/; the SPA may be stale');
  } else {
    log('[ui-shots] building SPA ...');
    buildSpa();
    copyDistToWebdist();
  }

  const workDir = fs.mkdtempSync('/tmp/ui-shots-run-');
  const proxyLog = path.join(workDir, 'proxy.log');
  const mockLog = path.join(workDir, 'mock.log');
  log(`[ui-shots] workdir = ${workDir}`);

  const mockPort = await freePort();
  const proxyPort = await freePort();
  const base = `http://127.0.0.1:${proxyPort}`;

  log('[ui-shots] starting mock upstream + proxy ...');
  start('mock upstream', 'python3', [MOCK_UPSTREAM, String(mockPort), '--fail-first', '2', '--fail-status', '500'], {
    log: mockLog,
  });

  const build = spawnSync('go', ['build', '-o', path.join(workDir, 'llm-proxy'), '.'], {
    cwd: ROOT,
    encoding: 'utf8',
  });
  if (build.status !== 0) {
    die('go build failed:\n' + ((build.stdout || '') + (build.stderr || '')).trimEnd().split('\n').slice(-30).join('\n'));
  }

  const cfgPath = writeConfig(workDir, proxyPort, mockPort);
  start('proxy', path.join(workDir, 'llm-proxy'), ['serve', '--config', cfgPath], { log: proxyLog });

  // Poll for readiness -- never sleep a fixed interval and hope.
  if (!(await waitForHTTP(`${base}/healthz`, 20000))) {
    die(`proxy never became ready on ${base} (polled /healthz for 20s).`, `\n--- proxy.log ---\n${tailFile(proxyLog)}\n`);
  }
  log(`[ui-shots] proxy ready on ${base} (mock upstream on ${mockPort})`);

  if (!(await waitForHTTP(`${base}/api/overview`, 10000))) {
    die(`/api/overview never answered 200.`, `\n--- proxy.log ---\n${tailFile(proxyLog)}\n`);
  }

  await seedTraffic(base);
  log('[ui-shots] seeded mock traffic; capturing ...');

  const { written, consoleErrors } = await capture(opts.out, base, shots, opts);

  for (const p of written) process.stdout.write(p + '\n');
  log(`[ui-shots] wrote ${written.length} file(s) to ${opts.out}`);

  if (consoleErrors.length) {
    warn(`${consoleErrors.length} console/page error(s) during capture:`);
    for (const e of consoleErrors.slice(0, 20)) warn(`  ${e}`);
  } else {
    log('[ui-shots] no console errors during capture');
  }
  if (written.length === 0) die('no screenshots were written');
}

main()
  .then(() => {
    // Explicit exit: our own child processes (mock upstream, proxy) hold open
    // stdio handles that would otherwise keep the event loop alive forever, so
    // node would hang after a fully successful run. The 'exit' handler still
    // fires and SIGTERMs both children.
    process.exit(0);
  })
  .catch((e) => die(`unhandled: ${e && e.stack ? e.stack : e}`));
