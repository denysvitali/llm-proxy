# Working agreements

- Use clear, casual communication with the user; emoji are welcome.
- Work only in the primary checkout on the default `main`/`master` branch. Never create branches or Git worktrees. Stop if switching would endanger existing changes.
- Commit task changes with Conventional Commits and push to the configured remote before declaring completion. Exclude unrelated user changes; report failed pushes.

# Dashboard development

The dashboard is in `web/` (React, TypeScript, Mantine). Go embeds
`internal/server/web/webdist/`. After changing the UI, run
`scripts/build-web.sh` and commit the rebuilt assets along with the source.
Run `npm --prefix web run lint`; the build includes TypeScript checking.

Keep navigation, keyboard access, light/dark themes, and narrow screens usable.
Distinguish unknown statistics from zero. Go's empty slices can arrive as JSON
`null`: normalize nullable collections at the API boundary. In particular,
ZCode can return `plans: null`; it must render the empty usage state without
crashing the home page. The page error boundary must leave the shell usable.

# Browser screenshots and visual review

Use local Playwright + Chromium for cluster addresses. The reference service is
`http://llm-proxy.llm-proxy.svc.cluster.local`; it resolves within this workspace.
A hosted browser does not share this cluster's DNS/network by default.

The reusable harness supports an existing URL without building, starting a
server, or sending seed traffic:

```bash
node scripts/ui-shots.mjs --base-url http://llm-proxy.llm-proxy.svc.cluster.local --out /tmp/llm-proxy-reference --only providers-light,providers-mobile
```

For local edits with live API data, start Vite in a separate terminal:

```bash
cd web
LLM_PROXY_API_TARGET=http://llm-proxy.llm-proxy.svc.cluster.local npm run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

Then, from the repository root:

```bash
node scripts/ui-shots.mjs --base-url http://127.0.0.1:5173 --out /tmp/llm-proxy-review --regression
```

- `--list` shows shot IDs; `--only home-light,home-dark,home-mobile` narrows them.
- `--full-page` captures all content; use viewport captures too, to assess what is visible without scrolling.
- Open the PNGs with the available image-viewing tool and inspect them. A successful HTTP response or screenshot file alone does not prove the app rendered.
- Review all four pages, desktop light/dark, mobile, and model/provider drawers. Verify navigation into Overview from another page, not just direct loading.
- `--regression` mocks read APIs to test null/missing/empty/populated ZCode plans, null activity feeds, filtering, API failures, and error-boundary recovery. It does not mutate the target service.
- The harness fails on uncaught page errors, missing page headings, or horizontal page overflow. Do not accept blank screenshots as successful references.
- Without `--base-url`, the harness builds and starts an isolated Go proxy with a mock upstream and seeds only that local proxy. This verifies embedded production assets independently of the live deployment.

The workspace's known-good browser configuration is in `scripts/ui-shots.mjs`:
Playwright comes from the npm cache and Chromium is the cached
`chromium_headless_shell-1234` binary. `PLAYWRIGHT_MODULE` and
`CHROMIUM_EXECUTABLE` can override these paths.

Scope `LD_LIBRARY_PATH=/tmp/pwchrome-libs` to the browser process only, never
Node or the shell. Do not bulk-copy libraries into that directory: mixed ABIs
cause segmentation faults. The harness adds only missing libraries and uses
`/tmp/fonts.conf` for font rendering. See `scripts/ui-shots/README.md` for details.

Do not commit screenshots containing live account or operational data. Keep
review artifacts under `/tmp` and link them in the result. Do not send inference,
change configuration, or run sign-in flows against the reference service during
visual review.
