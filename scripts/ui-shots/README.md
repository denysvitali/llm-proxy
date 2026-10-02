# UI screenshot harness

Shoots the dashboard SPA as served by the real Go binary against the repo's mock
upstream, so the PNGs reflect `web/src/` without needing Vite's dev server.

## Re-shoot after editing `web/src/`

```bash
node scripts/ui-shots.mjs --out /tmp/ui-shots/current
```

That's the whole recipe: it builds the SPA, boots mock upstream + proxy on free
ports, waits for `/healthz`, seeds traffic, shoots, prints one absolute path per
line to stdout, and tears down its own processes.

- default output `/tmp/ui-shots/current`; `--only home-light,models-drawer` for a
  subset; `--list` prints ids; `--skip-build` reuses the existing `web/dist`.
- **After a real `web/` change the lead must still commit the rebuilt
  `internal/server/web/webdist/`** — the harness only overwrites it locally.

21 shots: `/`, `/models`, `/providers`, `/setup` x (desktop light, desktop dark,
mobile light, mobile dark) + home scrolled to the footer + the open drawer on models and
providers, on desktop and mobile.

## Two segfault traps — do not "clean these up"

The headless browser needs glib-family libs this box doesn't ship. The harness
builds a **minimal** shim dir at `/tmp/pwchrome-libs` and passes it to the
browser only.

1. **Never bulk-copy libs into `/tmp/pwchrome-libs`.** Dumping every lib from
   the vm-lab/firmware trees (~1431 files) segfaults chrome (exit 139) — mixed
   ABI copies shadow the good system libs. Copy *only* what `ldd <browser>`
   reports as `=> not found`, re-globbing each round since new libs pull in
   transitive deps.
2. **Never put `LD_LIBRARY_PATH=/tmp/pwchrome-libs` in node's own env.** That
   segfaults *node* (exit 139). Scope it to the browser process only, via
   `chromium.launch({ env: {...process.env, LD_LIBRARY_PATH: '/tmp/pwchrome-libs', ...} })`.

Known-good browser binary (`chrome-headless-shell-linux64`, **not** `chrome-linux`):

```
/home/workspace/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell
```

Playwright: `/home/workspace/.npm/_npx/e41f203b7505f1fb/node_modules/playwright`.

## Existing pages and regressions

`--base-url URL` uses an existing server in read-only mode: no build, local
processes, or seed traffic. This works with the internal cluster service or
Vite configured with `LLM_PROXY_API_TARGET`. See the root `AGENTS.md` for the
complete live-reference and local-preview commands.

`--full-page` captures the complete page height. `--regression` also runs mocked
browser checks for home-page navigation with nullable ZCode plans, null activity
feeds, provider search, failed APIs, and render-error recovery.

MiniMax account regressions also cover zero versus unknown credits, nullable
check-in days, included bonus points, quota resets and unlimited allowances,
partial account failures, and already-claimed rewards. Explicit check-in clicks
use intercepted POST requests: no check-in reaches the supplied server, and the
checks verify that loading a page never claims credits or retries a failed claim.
Four `minimax__*.png` card screenshots show populated seven-day schedules in
desktop/mobile light/dark states. Mobile card captures use extra viewport height
to keep fixed navigation clear of the content; `minimax-viewport__*.png` retains
the actual 390×844 view for usability review. `minimax-drawer__*.png` also captures
the account within the provider drawer in all four views. All artifacts use the
requested output directory.

Captures fail for uncaught page errors, missing headings, horizontal overflow,
or drawers that fail to open. Inspect the actual PNGs after a successful run.
The default build now runs `npm run build`, including TypeScript checking.

Override `PLAYWRIGHT_MODULE` or `CHROMIUM_EXECUTABLE` to use another installed
toolchain. The library shim is scoped to Chromium only.

The generated mock config sets `stats.persist_file: ""` so it cannot load the
shared `~/.local/state/llm-proxy/stats.json` from unrelated local runs.

Setup and shell checks cover client tabs, exact snippet copying, line wrapping,
explicit health checks and failure recovery, plus keyboard navigation that
ignores typing, modified keys, and open dialogs. Health responses are intercepted;
these checks never send inference. Catalog checks cover pagination, provider
filtering, empty results, and model/provider detail navigation.
