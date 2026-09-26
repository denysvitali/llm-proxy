# llm-proxy dashboard — design system

**Read this before you touch a file in `web/src/`.** It is the contract. If your
component contradicts something here, you are wrong; if the contract is wrong,
raise it with the lead rather than silently diverging.

## 1. The direction in one paragraph

A **dark-first, dense operator control room**. Near-black layered surfaces, cards
one step above the canvas and separated by a **hairline rather than a shadow**,
one saturated blue accent used sparingly for interaction (never for data), and
**tabular monospace numerics** everywhere a number can be compared vertically.
It should feel like a well-made terminal-adjacent ops tool — precise, quiet, and
dense — not a consumer dashboard. Information density is a feature; every pixel
should carry signal.

**Feels like:** Linear / Vercel / Grafana-at-midnight. Calm, technical, fast.
**Does not feel like:** frosted glass, big rounded marketing cards, floating
blobs, drop shadows, gradients-as-decoration, oversized hero numbers, playful
illustration, generous whitespace for its own sake.

### Two rules that override everything else

1. **Density over decoration.** A screen with more usable signal per scroll wins.
2. **Hairline over shadow.** On near-black, a shadow is invisible and a border is
   the only honest separator. Use `1px solid var(--hairline)`.

## 2. Color tokens

Defined in `web/src/index.css` per color-scheme, and mirrored as Mantine ramps in
`theme.ts` (`colors.brand`, `colors.dark`).

| Token | Light | Dark | Use |
|---|---|---|---|
| `--canvas` | `#f4f5f7` | `#0b0d10` | `body` background |
| `--card` | `#ffffff` | `#14161a` | Card/Paper surface — **the exact value charts are validated against** |
| `--sunken` | `#eceef1` | `#101318` | Code blocks, inset wells |
| `--hairline` | `rgba(0,0,0,.10)` | `rgba(255,255,255,.10)` | All borders/dividers |
| `--segmented-track` | `rgba(0,0,0,.06)` | `rgba(255,255,255,.07)` | Segmented control track |
| `--segmented-thumb` | `#ffffff` | `#272d36` | Segmented control selected |
| `--chart-grid-color` | `rgba(0,0,0,.08)` | `rgba(255,255,255,.09)` | Chart gridlines |
| `--chart-cursor-fill` | `rgba(0,0,0,.04)` | `rgba(255,255,255,.06)` | Chart crosshair band |

**Never hard-code a hex for a surface.** Use `var(--card)`, `var(--hairline)`,
`var(--canvas)`, or Mantine semantic colors (`--mantine-color-text`,
`-dimmed`, `-default-border`). A hard-coded hex is a bug: it will not follow the
scheme.

### Accent (interaction only — never data)

`brand` ramp in `theme.ts`; primary shade is `6` on light, `8` on dark. Use it
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

- UI/body: `-apple-system, "Segoe UI", Roboto, Inter, system-ui, sans-serif`
- **Everything numeric**: `var(--numeric-font)` (the mono stack) **+**
  `font-variant-numeric: tabular-nums`.
- `index.css` already applies tabular figures to all `<Table>` cells. For a stat
  tile / headline number / bar label, add the `stat-value` class (mono + tnum +
  slight negative tracking). **A number a human scans vertically must not
  shimmer in width.**
- Scale: `xs 11.5 · sm 13 · md 14 · lg 16`; headings `h1 22 → h5 13`, weight
  600–650. Headings get `letter-spacing: -0.014em` automatically.
- Line lengths: cap prose at ~70ch. Denseness comes from structure, not from
  shrinking type below 13px.

**This is the canonical type scale.** `theme.ts` has been updated to match
exactly: `xs: 11.5, sm: 13, md: 14, lg: 16, h1: 22`. Do not deviate.

## 4. Spacing / radius / elevation

- **Spacing** is the Mantine scale: `xs 6 · sm 10 · md 16 · lg 24 · xl 32`.
  Page sections sit at `lg`; a dense row at `xs`/`sm`.
- **Radius**: `xs 2 · sm 4 · md 6 · lg 10 · xl 14`. Default is `md`. Data-dense
  surfaces prefer `sm`/`md`; only a large panel earns `lg`. **No pill shapes**,
  no 20px+ radii.
- **Elevation**: no `box-shadow` on cards. Use a border. A shadow is only
  acceptable for a genuinely floating layer (popover/menu).

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
- **Legend for ≥2 series**, and ≤4 series are also direct-labeled. One series
  needs no legend — the title names it.
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
| Tabular mono for every number | Proportional figures in a scanned column |
| Hairline borders | Drop shadows on cards |
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
`lint` has pre-existing `only-export-components` warnings in
`GrokUsageCard`, `HistoryCharts`, `Overview` — leave those alone; just don't add
new ones.
