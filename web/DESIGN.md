# llm-proxy dashboard design

Waypoint is a wayfinding console on Mantine, dressed in the EasyUI
(easyui.site) system: near-white paper light, near-black harbour dark, ink as
the interaction accent, one reserved blue for live traffic, spring motion, and
real elevation instead of a wall of hairlines. Tokens live in `index.css`,
theme wiring in `theme.ts`, motion vocabulary in `motion/tokens.ts`, shell
chrome in `app-shell.css`. Do not hard-code surface colors in components. Do
not add a second component library.

## Layers, not lines

Everything sits on a named elevation:

| Token | Light | Dark | Use |
|---|---|---|---|
| `--canvas` | `#fafafa` | `#111113` | page behind cards |
| `--card` / `--surface-1` | `#ffffff` | `#171a21` | ordinary card |
| `--surface-2` | `#f7f7f8` | `#1c1c1f` | raised block inside a card |
| `--sunken` / `--surface-3` | `#f4f4f5` | `#1a1a1d` | inputs, tracks, inset panels |
| `--hairline` | `#e4e4e7` | `#2a2a2e` | 1px separation |
| `--hairline-subtle` | `#ededef` | `#232327` | shell rails, footer rule |

Shadows are named by elevation: `--shadow-subtle` (resting chip),
`--shadow-raised` (a card), `--shadow-lift` (a hovered card), `--shadow-overlay`
(drawers, dialogs, popovers). A card is `1px --hairline` plus `--shadow-raised`;
hover on something clickable lifts to `--shadow-lift` and a slight `translateY`
(see `.lift`). Never use a colored or gradient drop shadow.

Ink is the accent. `--accent-brand` is `#0a0a0a` in light and `#f2f2f3` in dark;
`--accent-soft` is its 5–7% wash for selected rows and icon tiles;
`--accent-ring` is the focus color. The one blue (`#3b82f6`,
`--indicator-active`) belongs to the live-traffic indicator only — never to a
button, nav item, or chart series.

**Dark card stays `#171a21`.** `palette.ts` chart colors were validated against
exactly `#ffffff` (light) and `#171a21` (dark). Changing a card surface means
re-running the dataviz validator before shipping. Never paint a card or plot
background with a series color. Do not recolor series when a filter changes. A
fifth series aggregates to Other through the existing palette helper.

Semantic status (`--data-good`, `--data-warning`, `--data-serious`,
`--data-critical`) is separate from chart series and always ships with an icon
or a text label, never color alone.

## Type and shape

Geist (vendored, OFL) for UI; JetBrains Mono for numbers, IDs, and code.
Body tracking `-0.011em`, headings `-0.02`…`-0.035em`. The Waypoint micro-label
survives: 11px, weight 650, `+0.08em` tracking, uppercase, muted — section
heads, chart titles, stat labels, table column heads. Page titles are
`clamp(22px, 2.4vw, 26px)`/700. Measured numbers use tabular figures. Model IDs
wrap with `overflow-wrap: anywhere` and are never truncated.

Radii: 6px controls-inline, 8px controls, 10px chips/rows, 14px cards,
20px feature panels, 999px pills. Focus-visible is a 2px `--accent-ring` ring
with a 2px offset (`.focus-ring`). Interactive targets stay ≥44px on coarse
pointers. The shell keeps a skip link, `#main`, and a pathname-keyed page error
boundary.

## Motion

Springs, not linear tweens, and identical feel everywhere. `motion/tokens.ts`
exports `springSnappy` (hover/press), `springMorph` (tab pills, layout ids),
`springGentle` (panels, page veil), `springSmooth` (card entrances),
`easeSoft`/`easeFast` for color and opacity. CSS mirrors them as
`--motion-fast` (140ms), `--motion-base` (220ms), `--motion-slow` (320ms) with
`--ease-out: cubic-bezier(.22,1,.36,1)`.

Rules: motion explains a change (a pill sliding to the selected tab, a row
arriving in a live feed, a page veil during loading) — it is never ambient
decoration. Entrances are 6–10px rises with fades, staggered ≤45ms. Every
animated behavior calls `useMotionPreset()` (or checks the media query in CSS)
and collapses to instant under `prefers-reduced-motion`. No layout animation on
every keystroke; no parallax, no glow, no gradient slabs.

The shell adds: a 2px route rail on navigation, a page transition (opacity +
6px rise), a loading veil (`Fade`) that dims and blurs content while the
primary queries settle, and a ⌘K command palette (pages, resources, shortcut
help). The live-traffic badge blips on each arriving request.

## Shell

One top bar, 60px desktop / 52px mobile: brand (ink tile with an activity
glyph), desktop section tabs with a shared 2px active pill, and actions
(command trigger, live badge, shortcuts, color scheme) on the right. Below
`48em` the section tabs disappear, the sidebar is replaced by a bottom bar with
a raised active-icon pill, and the command palette rides the keyboard button.
Keep the classes `.bottom-navigation`, `.sidebar-nav`, `.sidebar-caption`,
`.sidebar-footer`, `.brand-link`, `.brand-mark`, `.brand-text`, `.live-status`,
`.app-topbar`, `.topbar-inner`, `.topbar-actions`, `.page-container`,
`.app-footer`, `.page-header`, `.page-title`, `.page-subtitle`, `.page-loading`.

Nav items are an icon plus a label with a clear active state
(`aria-current="page"`). Keyboard: `g` then `o`/`m`/`p`/`s`, `?` opens
“Keyboard shortcuts”, `⌘K`/`Ctrl+K` opens the command menu. No marketing
chrome: no hero, decorative network diagram, gradient slabs, or theme-invariant
surfaces.

## Pages

Overview’s `h1` is “Overview”. First band: a hero stat composition (requests
with a sparkline trend, then success rate, tokens, median tok/s) with a quiet
meta line (models with traffic, tool calls/errors). Then “Performance over
time”: four always-visible charts (requests, success rate, latency, tokens) in
one instrument panel, each with a tabular headline, a measure caption and a
delta vs the previous interval where samples allow; 4-up ≥1280px, 2-up below,
1 column on mobile. Then “Subscription usage” as an always-visible grid of
account cards on desktop (collapsible only on mobile; cards hug their content;
`plans: null` renders the empty usage state, never a crash). Then “Recent
activity”: recent requests and upstream errors as two fixed-height scroll
feeds (sticky headers, ~320px) side by side ≥80em, stacked below. Then
“Gateway readiness” as a compact status matrix (dot + label + provider id) with
attention counts — never a tall empty column. Then “Traffic breakdown”: top
models by requests (explicit “+N more models” link) beside a dense provider
health list.

Models’ `h1` is “Models”. Visible tabs stay “Available models” and “Recorded
traffic”. Search label is “Find an available model”. The table class is
`.models-table`. Catalog entries keep `.catalog-model-entry` and the copy
button's accessible name `Copy <id>`; the grid keeps `.catalog-model-grid`.
Pagination reads like “1–18 of 40 available models” with “Next catalog page”.
The details drawer title closes with “Close model details”, loading is
“Loading model details”, the provider filter is “Filter available models by
provider”. The drawer scrolls internally, never ends in dead space, and is
full-width on mobile. “All recorded traffic” stays a heading inside the drawer,
and the History tab still shows “Performance history”.

Providers’ `h1` is “Providers”. Search label is “Search providers”. The health
combobox is “Filter provider health” and includes “All providers” and “Needs
attention”. Counts read “1 of 2 providers” / “2 of 2 providers”; the empty
filter state is “No matching providers”. Inspect buttons are named
“Inspect …”. The drawer closes with “Close provider details” and loads with
“Loading provider details”. Provider cards are an equal-height responsive grid:
three columns wide desktop, two before that, one on mobile.

Setup’s `h1` stays “Setup” (the harness asserts it). The section heading is
“Choose your client”. Buttons: “Copy Claude Code snippet”, “Copy Codex CLI
snippet”, “Check connection”. Tabs: “Codex CLI”, “HTTP / curl”. Regions:
“Codex CLI setup snippet”, “Other / curl setup snippet”. The switch is
“Wrap Other / curl snippet lines”; wrap off keeps `white-space: pre`. Health
copy is exact: “Gateway is reachable.”, “Gateway returned HTTP 503.”,
“Could not reach the gateway. Check your connection and try again.”
The check is `GET /healthz` on click only. Never call inference or `/v1` on
load. Snippets render on a dark terminal surface in both schemes. The numbered
steps keep `.setup-sequence` compact on mobile (under 110px tall).

## Unknown versus zero

An unknown statistic is an em dash (`—`), never `0`, `0%`, or an empty chart
that implies a measured zero. Loading uses skeletons that match the loaded
layout. Empty states say what is missing. A failed request is an error with
retry, not a zero. A delta pill renders only when both compared samples exist.

## Charts

- The server zero-fills idle intervals. `normalizeStatsSeries` turns success
  rate and percentile points in zero-request intervals into `NaN`, so they plot
  as gaps and read “No sample”; counts keep their real zeros.
- Thin marks: 1.5–2px strokes, no data dots on long series, one dot for the
  latest sample, recessive grid (horizontal only), 11px muted tabular ticks.
- No dual axes. Grid lines use `--chart-grid-color`. Plot background is the card
  surface. Series names and order come from the data, colors from
  `colorForSeries`.
- Latency charts say whether the value is p50.
- Every chart with data keeps a hover scrubbing tooltip plus the “View data”
  table for keyboard/touch inspection.
- Single-series charts may take an area wash; multi-series stay crisp lines so
  overlapping series never muddy each other.

## Copy that must survive

“Reload page”, “This page couldn't be displayed”, “No plan usage is available
for this account.”, “Regression plan”, “No recent upstream errors”,
“Request stats unavailable”, “No model traffic yet”, “All recorded traffic”,
“Performance history”, “Live updates”, “Couldn't refresh upstream errors”,
“Couldn't load request details”, “This request is no longer available in the
retained history.”, “Retry”, “View available offers”, “Offer claimed”.

ZCode `plans: null` renders the empty usage state and must not crash Overview.
Normalize nullable collections at the boundary (`?? []`). One page-level error
when every overview query fails; partial failures keep their own retry.

## Accessibility

- Name icon-only buttons.
- Do not rely on color alone for status; pair it with text and an icon.
- Honor `prefers-reduced-motion` everywhere (motion tokens and CSS alike).
- Keep light and dark themes and 320px layouts free of horizontal page
  overflow.
