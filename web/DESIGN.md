# llm-proxy dashboard — design system

**Read this before you touch a file in `web/src/`.** It is the contract. If your
component contradicts something here, you are wrong; if the contract is wrong,
raise it with the lead rather than silently diverging.

## 1. The direction in one paragraph

A compact gateway workspace with a persistent slate sidebar, indigo interaction
accents, and a clear hierarchy from page title to sections to data. Light and
dark schemes share the same layouts. Cards use quiet borders and restrained
rounding; charts and tables keep the emphasis on readable values.

Desktop navigation is 232px wide. Mobile uses bottom navigation and shows the
current page below the brand. Keep the first screen useful: show key statistics,
primary controls, and the start of the main content before secondary details.

Prefer structure over decoration: group related controls, disclose dense detail,
and preserve complete model IDs. Gradients are limited to the small brand mark.

## 2. Color tokens

Defined in `web/src/index.css` per color-scheme, and mirrored as Mantine ramps in
`theme.ts` (`colors.brand`, `colors.dark`).

| Token | Light | Dark | Use |
|---|---|---|---|
| `--canvas` | `#f5f7fb` | `#0c111d` | `body` background |
| `--card` | `#ffffff` | `#14161a` | Card/Paper surface — **the exact value charts are validated against** |
| `--sunken` | `#eff2f8` | `#1a2539` | Code blocks, inset wells |
| `--hairline` | `#e2e7f0` | `#26324a` | All borders/dividers |
| `--segmented-track` | `#edf0f7` | `#1a2539` | Segmented control track |
| `--segmented-thumb` | `#ffffff` | `#303b53` | Segmented control selected |
| `--chart-grid-color` | `rgba(37,51,78,.08)` | `rgba(196,210,236,.10)` | Chart gridlines |
| `--chart-cursor-fill` | `rgba(37,51,78,.04)` | `rgba(196,210,236,.06)` | Chart crosshair band |

The sidebar has its own fixed slate surface and foreground tokens in `index.css`.
Other components must follow the selected scheme. **Do not hard-code surface hexes in components.** Use `var(--card)`, `var(--hairline)`,
`var(--canvas)`, or Mantine semantic colors (`--mantine-color-text`,
`-dimmed`, `-default-border`). A hard-coded hex is a bug: it will not follow the
scheme.

### Accent (interaction only — never data)

`brand` ramp in `theme.ts`; primary shade is `6` on light, `4` on dark. Use it
for active nav, primary buttons, focus rings, links. **Never use it as a chart
series color.**

### Chart palette — DO NOT TOUCH, DO NOT EYEBALL

`web/src/palette.ts` is machine-validated. Get colors from
`useChartPalette()` (`{ dark, series, ramp, magnitude, surface }`); never import
a hex directly into a component.

| | light | dark |
|---|---|---|
| `categorical` | `#036eae` `#48a260` `#8a3400` `#8b54a2` | `#0083cf` `#3aa85b` `#af4803` `#8c4ca6` |
| `ordinal` (one hue) | `#8cb2dd` `#4383c8` `#00539a` | `#1d4f82` `#2773c0` `#649cda` |
| `magnitude` | `#036eae` | `#0083cf` |
| `status` | good `#1a9e5f` · warning `#c98a00` · serious `#e07b1a` · critical `#d63b30` | same (validated per surface) |

Validator verdicts (all PASS, no warn/relief bands): categorical light worst
adjacent CVD ΔE **19.3**, normal-vision **20.6**; dark **11.8** / **21.2**.

**If you must change a chart color, run the validator** — do not eyeball:
```
node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode light --surface #ffffff
node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode dark  --surface #14161a
node <dataviz>/scripts/validate_palette.js "<hex,...>" --mode light --surface #ffffff --ordinal
```

### Status color duality

There are two sets of status colors, intentionally divergent:

- **`palette.ts` status colors** — saturated, for chart data points. These are
  machine-validated for contrast against the card surface.
- **`index.css` `--data-*` colors** — muted, for UI badges and indicators.
  These are designed for small text and icons on card surfaces.

Never use a chart status color for a UI badge, or vice versa.

### Dark mode toggle

- `"auto"` follows `prefers-color-scheme`.
- Transition is a 150ms crossfade on `background-color` only.

## 3. Type

- UI/body: Inter with system fallbacks; monospace for code and identifiers.
- Use tabular figures for numbers compared in columns (`tabular` or `stat-value`).
- Scale: `xs 12 · sm 13 · md 14 · lg 16`; page titles are 32px on desktop
  and 28px on mobile. Section titles remain 15–18px.
- Keep paragraph line lengths readable. Do not reduce text sizes to fit a layout;
  let controls wrap or switch to a single column.

## 4. Spacing / radius / elevation

- Mantine spacing: `xs 6 · sm 10 · md 16 · lg 24 · xl 32`.
- Radius: `xs 2 · sm 4 · md 8 · lg 14 · xl 18`; default controls use `md`,
  cards use `lg`. Badges can be rounded. Avoid nested oversized cards.
- Borders use `var(--hairline)`. The shared card shadow is subtle; do not add
  independent shadows or animated lifts to static data cards.

## 5. Chart rules (hard rules)

- **No dual-axis charts. Ever.** Two measures of different scale ⇒ two charts,
  small multiples, or index to a common base. This is the single most common
  chart mistake.
- **Form follows the job**: magnitude → horizontal bar; identity → categorical
  color in fixed order; ordered data (percentiles) → the one-hue `ordinal`
  ramp; polarity → a two-hue diverging scale with a neutral gray midpoint.
- **Color follows the entity, never its rank.** Slot 0 is always `input`, slot 1
  `output`, slot 2 `cache read`, slot 3 `cache write` (`seriesNames`). A filter
  that removes a series must not repaint the survivors. A 5th series folds into
  "Other" or becomes small multiples — never a generated hue.
- **Persistent named keys for multiple series.** History charts show the latest
  sampled value beside each key above the plot. Do not place end labels inside
  narrow plots, where they overlap. Keep tooltips and accessible data tables.
- **Hover layer is mandatory**: crosshair + tooltip on line/area, per-mark
  tooltip on bar/dot/cell. Hit target ≥ the mark.
- **Grid and axes are recessive**; never a number on every point. Selectively
  direct-label the extremes.
- **State is never color alone** — always icon or text too.

## 6. Motion

Fast and functional. Durations `120ms` (hover/tint) and `200ms` (drawer/modal);
easing `ease-out` for entrances, `ease-in` for exits. Animate `opacity`,
`transform`, and `background-color` only — never `width`/`height`/`top`.
`prefers-reduced-motion` is already wired globally; don't fight it.

## 7. Responsive

- `48em` (768px) is the single breakpoint. Below it: AppShell shows a bottom tab
  bar (`App.tsx` owns it — **your CSS must not fight it**), tables become cards,
  multi-column grids collapse to one column, drawers go full-width.
- Touch targets ≥44×44px on coarse pointers.
- `overflow-x` on data tables; long identifiers use `overflow-wrap: anywhere`
  and MUST NOT be truncated — the model name is the identity.

## 8. Component contracts you must preserve

Other pages import these. Do not rename props.

```ts
PageHeader   ({ title, subtitle?, extra? })
PageSection  ({ title, description?, extra? })
EmptyState   ({ icon, title, hint? })
```

Pages also share `StatTile`, `UptimeBadge`, `StatusChips`, `TokenMixBar` /
`TokenLegend`, `PercentileBars`, `TimeRangeControl`, `HistoryCharts`. Read the
component before using it; if you change a signature, grep for every caller and
update them in the same commit.

**`Fade`** is exported from `App.tsx` (`{ pending, children }`) and imported by
all four pages. Leave that export alone.

## 9. Do / Don't

| Do | Don't |
|---|---|
| Use `var(--card)`, `var(--hairline)` | Hard-code surface hexes |
| Tabular figures for comparable values | Proportional figures in a scanned column |
| Quiet shared borders and elevation | Per-component shadows or hover lifts |
| Fixed categorical slot order | Assign colors by rank/sort order |
| One axis per chart | Dual-axis charts |
| Icon **or** label with status color | Color-only state |
| `overflow-wrap: anywhere` on identifiers | Truncating model names |
| Reuse shared components | Forking a near-copy per page |
| Re-run the validator if you touch chart color | Eyeballing palette changes |

## 10. Accessibility floor

- Body text ≥4.5:1; large text and UI edges ≥3:1.
- Visible `:focus-visible` ring on everything interactive (2px, 2px offset).
  Do not `outline: none` without a replacement.
- Every icon-only control needs an `aria-label`.
- Filters/results announce via `aria-live`; charts expose a text/table
  equivalent or a complete `aria-label`.
- Never convey state by color alone.

## 11. Z-index scale

| Layer | z-index |
|---|---|
| base | 0 |
| sticky | 10 |
| dropdown | 100 |
| drawer | 200 |
| modal | 300 |
| toast | 400 |
| skip-link | 500 |

## 12. Skeleton pattern

- Use `var(--sunken)` background with a subtle pulse animation.
- Match the dimensions of the content being loaded.
- No spinner for initial load — use skeleton placeholders.

## 13. Toast / notification pattern

- Placement: bottom-right.
- Duration: 4s for success, 6s for error.
- Variant colors: success → `--data-good`, error → `--data-critical`.

## 14. Form input styling

- Radius: `md`.
- Border: `1px solid var(--hairline)`.
- Focus ring: `2px solid var(--mantine-primary-color-filled)`.
- Error state: `border-color: var(--data-critical)`.

## 15. Error state pattern

- Icon + message + retry action.
- Use `--data-critical` for critical failures.
- Use `--data-warning` for recoverable errors.
- Never show a wall of identical error cards — consolidate into a single
  banner when all queries fail.

## 16. Icon size scale

| Size | Value | Use |
|---|---|---|
| xs | 12px | inline text |
| sm | 14px | buttons |
| md | 16px | nav |
| lg | 20px | standalone |
| xl | 24px | hero |

## 17. Verify before you finish

```bash
cd web && npx tsc -b          # must pass
cd web && npm run lint        # no new errors
node scripts/ui-shots.mjs --out /tmp/ui-shots/current   # shoot your pages
```
`lint` has two existing `only-export-components` warnings in `HistoryCharts`.
Do not introduce new warnings. Run the regression harness for behavior changes:

```bash
node scripts/ui-shots.mjs --base-url http://127.0.0.1:5173 --regression --out /tmp/llm-proxy-review
```

Page orchestration lives in `pages/`; detailed views and helpers live in
`components/catalog/`, `components/overview/`, and `components/setup/`. Keep
page styles in the corresponding CSS file. Usage cards share `UsageCardShell`
and `UsageMeter`. Setup connection checks must use `GET /healthz`; loading the
page or checking connectivity must never send inference or claim rewards.
