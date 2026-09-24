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

- default output `/tmp/ui-shots/current`; `--only home,models-drawer` for a
  subset; `--list` prints ids; `--skip-build` reuses the existing `web/dist`.
- **After a real `web/` change the lead must still commit the rebuilt
  `internal/server/web/webdist/`** — the harness only overwrites it locally.

15 shots: `/`, `/models`, `/providers`, `/setup` x (desktop light, desktop dark,
mobile dark) + home scrolled to the footer + the open drawer on models and
providers.

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

## Deliberate, don't "fix"

- The build is `vite build` only — **`tsc -b` is skipped** (it currently fails on
  unused imports in `web/src/theme.ts`), so type errors won't fail the run. The
  harness prints a WARNING.
- The generated config sets `stats.persist_file: ""`; otherwise the proxy loads
  the shared `~/.local/state/llm-proxy/stats.json` and the dashboard shows other
  agents' leftover traffic instead of this run's.
