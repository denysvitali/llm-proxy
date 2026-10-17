# llm-proxy dashboard design

Signal is a dense operational console on Mantine. Tokens live in `index.css`
and map through `theme.ts`. Do not hard-code surface colors in components.
Do not add a second component library. The screenshot harness drives Mantine
comboboxes, tabs, and scroll areas.

## Color

Light: canvas `#eef1f6`, sidebar `#f6f7fa`, card `#ffffff`, border `#d5dbe6`,
ink `#141820`, muted `#5e6876`, accent `#2457d6`.

Dark: canvas `#0c0e13`, sidebar `#10131a`, card `#171a21`, border `#2c3240`,
ink `#f3f5f8`, muted `#a3adbd`, accent `#8eabff`.

Accent is for interaction (links, focus, active nav, primary buttons). It is
not a card fill and not a chart series. The only gradient is the small brand
mark. Cards are flat: 1px border, no drop shadow, no hover lift.

Semantic status (`--data-good`, `--data-warning`, `--data-serious`,
`--data-critical`) stays separate from chart series. Chart colors come only
from `palette.ts` / `useChartPalette()`. Never paint a card or plot background
with a series color. Do not recolor series when a filter changes. A fifth
series aggregates to Other through the existing palette helper.

Switching color scheme crossfades background and text over 150ms. Motion is
120ms for color and 200ms for opacity. `prefers-reduced-motion` disables both.

## Type and shape

Inter. Page titles are 22px/600 on desktop and 20px/600 below 48em. Section
titles are 14px/600. Do not shrink type to fit a layout. Controls use an 8px
radius; cards use 12px. Numbers that are measured values use tabular figures.
Model IDs wrap with `overflow-wrap: anywhere` and are never truncated.

Focus-visible is a 2px accent ring with a 2px offset. Interactive targets are
at least 44px on coarse pointers. The shell keeps a skip link, `#main`, and a
pathname-keyed page error boundary.

## Shell

Sidebar is 232px and theme-aware. Below `48em` (768px) it collapses and the
bottom navigation shows; at 769px and up the sidebar shows. Keep the classes
`.app-sidebar`, `.bottom-navigation`, and `.page-container`.

Nav items are an icon plus a label, with a clear active state. The product
name is “llm-proxy”. Keyboard shortcuts stay: `g` then `o`/`m`/`p`/`s`, and
`?` opens a dialog titled “Keyboard shortcuts”, with the existing focus and
search guards. The header theme control is a `SegmentedControl` labeled
“Color scheme”.

There is no marketing chrome: no “CONTROL CENTER”, workspace indexes, giant
hero, decorative network diagram, or theme-invariant slabs.

## Pages

Overview’s `h1` is “Overview”. The first content band is four `StatTile`s
(two-up on narrow screens): requests, success rate, tokens, median tok/s.
Then a history block in normal surface cards (plot about 150px, card near
240px) beside upstream errors, then “Subscription usage”, then recent
requests, then a compact gateway-readiness strip. Readiness must not open a
tall empty column. Recent-request inspection keeps `Inspect …` and
“Close request inspection”.

Models’ `h1` is “Models”. Visible tabs stay “Available models” and
“Recorded traffic”. Search label is “Find an available model”. The table
class is `models-table`. Catalog rows keep `catalog-model-entry` and
`[aria-label^="Open details for"]`. Pagination reads like
“1–18 of 40 available models” with “Next catalog page”. The details drawer
title closes with “Close model details”, loading is “Loading model details”,
and the provider filter is “Filter available models by provider”. The drawer
scrolls internally and is full-width on mobile. The History tab inside a
model still shows “Performance history”.

Providers’ `h1` is “Providers”. Search label is “Search providers”. The health
combobox is “Filter provider health” and includes “All providers” and
“Needs attention”. Counts read “1 of 2 providers” / “2 of 2 providers”; the
empty filter state is “No matching providers”. Inspect buttons are named
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
neither side is a dead band.

## Unknown versus zero

An unknown statistic is an em dash (`—`), never `0`, `0%`, or an empty chart
that implies a measured zero. Loading uses skeletons that match the loaded
layout. Empty states say what is missing. A failed request is an error with
retry, not a zero.

## Charts

- No dual axes.
- Grid lines use `--chart-grid-color`. Plot background is the card surface.
- Series names and order come from the data, colors from `colorForSeries`.
- Latency charts say whether the value is p50.

## Copy that must survive

“Reload page”, “This page couldn't be displayed”, “No plan usage is available
for this account.”, “Regression plan”, “No recent upstream errors”,
“Request stats unavailable”, “No model traffic yet”, “All recorded traffic”,
“Performance history”, “Live updates”, “Couldn't refresh upstream errors”,
“Couldn't load request details”, “This request is no longer available in the
retained history.”, “Retry”.

ZCode `plans: null` renders the empty usage state and must not crash Overview.
Normalize nullable collections at the boundary (`?? []`). One page-level error
when every overview query fails; partial failures keep their own retry.

## Accessibility

- Name icon-only buttons.
- Do not rely on color alone for status; pair it with text.
- Honor `prefers-reduced-motion`.
- Keep light and dark themes and 320px layouts free of horizontal page overflow.
