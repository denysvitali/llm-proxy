# llm-proxy dashboard design

Waypoint is a wayfinding console on Mantine: warm-paper light, harbor-dark,
teal interaction accent, uppercase micro-labels, tabular figures. Tokens live
in `index.css` and map through `theme.ts`. Do not hard-code surface colors in
components. Do not add a second component library. The screenshot harness
drives Mantine comboboxes, tabs, and scroll areas.

## Color

Light: canvas `#f4f3ef`, card `#ffffff`, header `#ffffff`, sunken `#edece6`,
hairline `#dcd8ce`, ink `#22211d`, muted `#6b6a63`, accent `#0f766e`.

Dark: canvas `#101613`, card `#151c17`, header `#1b241d`, sunken `#111812`,
hairline `#2a332c`, ink `#ecf1ec`, muted `#9aa79c`, accent `#4ecdb9`.

Accent is for interaction (links, focus, active nav, primary buttons, step
numbers). `--mantine-color-brand-light-color` maps to the accent so subtle and
light buttons read as links in both schemes. It is not a card fill and not a
chart series. Cards are flat: 1px hairline, no drop shadow, no hover lift.

Semantic status (`--data-good`, `--data-warning`, `--data-serious`,
`--data-critical`) stays separate from chart series. Chart colors come only
from `palette.ts` / `useChartPalette()`. The categorical palette was validated
against the white light card and the `#151c17` dark card — change a card
surface and re-run the dataviz validator before shipping. Never paint a card
or plot background with a series color. Do not recolor series when a filter
changes. A fifth series aggregates to Other through the existing palette
helper.

Switching color scheme crossfades background and text over 150ms. Motion is
120ms for color and 200ms for opacity. `prefers-reduced-motion` disables both.

## Type and shape

System sans stack (no webfont); mono is the ui-monospace stack for numeric and
identifier surfaces. The Waypoint voice is the micro-label: 11px, weight 650,
+0.08em tracking, uppercase, muted — used for section headings, stat labels,
chart card titles, summary labels, and table column heads. Page titles are
24px/700 on desktop and 20px/700 below 48em. Measured numbers use tabular
figures. Model IDs wrap with `overflow-wrap: anywhere` and are never
truncated.

Controls use a 6px radius; cards use 10px. Focus-visible is a 2px accent ring
with a 2px offset. Interactive targets are at least 44px on coarse pointers.
The shell keeps a skip link, `#main`, and a pathname-keyed page error
boundary.

## Shell

One top bar, 60px: brand (teal waypoint tile with a CSS diamond) on the left,
horizontal nav tabs in the middle with a 2px accent underline on the active
item, and actions (live badge, “Connect client”, keyboard shortcuts, color
scheme) on the right. Below `48em` (768px) the top nav disappears and the
bottom navigation shows; at 769px and up the top nav shows. Keep the classes
`.bottom-navigation`, `.topbar-navigation`, and `.page-container`.

Nav items are an icon plus a label, with a clear active state. The product
name is “llm-proxy”. Keyboard shortcuts stay: `g` then `o`/`m`/`p`/`s`, and
`?` opens a dialog titled “Keyboard shortcuts” (also reachable from the
top-bar keyboard button), with the existing focus and search guards. The
header theme control is a `SegmentedControl` labeled “Color scheme”.

There is no marketing chrome: no hero, decorative network diagram, gradient
slabs, or theme-invariant surfaces.

## Pages

Overview’s `h1` is “Overview”. The first content band is a single divided
stat band (`.stat-band`, one card, hairline gaps, two-up on narrow screens):
requests, success rate, tokens, median tok/s. A quiet meta row (models with
traffic, tool calls/errors) sits under it. Then a full-width history block of
four normal surface cards (plot about 150px; one row from 85em, two-up below,
one column on mobile). Compact chart legends name the marks only; the card
headline carries the values. Then “Subscription usage”, then “Recent
activity”: the recent-requests table (time, backend/model, wire, status)
beside upstream errors, stacked below 80em. Then a compact gateway-readiness
strip. Readiness must not open a tall empty column. Recent-request inspection
keeps `Inspect …` and “Close request inspection”.

Models’ `h1` is “Models”. Visible tabs stay “Available models” and “Recorded
traffic”. Search label is “Find an available model”. The table class is
`models-table`. Catalog rows keep `catalog-model-entry` and
`[aria-label^="Open details for"]`. Pagination reads like “1–18 of 40
available models” with “Next catalog page”. The details drawer title closes
with “Close model details”, loading is “Loading model details”, and the
provider filter is “Filter available models by provider”. The drawer scrolls
internally and is full-width on mobile. The History tab inside a model still
shows “Performance history”.

Providers’ `h1` is “Providers”. Search label is “Search providers”. The health
combobox is “Filter provider health” and includes “All providers” and “Needs
attention”. Counts read “1 of 2 providers” / “2 of 2 providers”; the empty
filter state is “No matching providers”. Inspect buttons are named
“Inspect …”. The drawer closes with “Close provider details” and loads with
“Loading provider details”. Providers are a wrapping card grid: three columns
on a wide desktop, two before that, one column on mobile.

Setup’s `h1` stays “Setup” (the harness asserts it). The section heading is
“Choose your client”. Buttons: “Copy Claude Code snippet”, “Copy Codex CLI
snippet”, “Check connection”. Tabs: “Codex CLI”, “HTTP / curl”. Regions:
“Codex CLI setup snippet”, “Other / curl setup snippet”. The switch is
“Wrap Other / curl snippet lines”; wrap off keeps `white-space: pre`.
Health copy is exact: “Gateway is reachable.”, “Gateway returned HTTP 503.”,
“Could not reach the gateway. Check your connection and try again.”
The check is `GET /healthz` on click only. Never call inference or `/v1` on
load. The snippet and the gateway/account details share one column flow so
neither side is a dead band. The numbered steps keep `.setup-sequence` as a
compact strip on mobile (under 110px tall) and anchor-scroll below the header.

## Unknown versus zero

An unknown statistic is an em dash (`—`), never `0`, `0%`, or an empty chart
that implies a measured zero. Loading uses skeletons that match the loaded
layout. Empty states say what is missing. A failed request is an error with
retry, not a zero.

## Charts

- The server zero-fills idle intervals. `normalizeStatsSeries` turns success
  rate and percentile points in zero-request intervals into `NaN`, so they
  plot as gaps and read “No sample”; counts keep their real zeros.
- No dual axes.
- Grid lines use `--chart-grid-color`. Plot background is the card surface.
- Series names and order come from the data, colors from `colorForSeries`.
- Latency charts say whether the value is p50.
- Marks stay thin (2px lines, max 18px bars, rounded data-ends), grid lines
  are recessive, and every chart with data keeps a hover tooltip plus the
  “View data” table for keyboard/touch inspection.

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
- Do not rely on color alone for status; pair it with text.
- Honor `prefers-reduced-motion`.
- Keep light and dark themes and 320px layouts free of horizontal page overflow.
