import { motion, useReducedMotion } from 'framer-motion'
import { useId, useLayoutEffect, useRef, useState } from 'react'

// A small, dependency-free sparkline for KPI tiles: a monotone-cubic line with
// a soft area wash, an end dot for the latest sample, optional pointer scrubbing
// (the nearest sample shows in the live region and as a thin cursor), and arrow
// key navigation. Missing samples (NaN) break the line instead of plotting as a
// measured zero — an absent value is never drawn as 0.
export interface SparklineProps {
  values: number[]
  color?: string
  height?: number
  labels?: string[]
  format?: (value: number) => string
  ariaLabel?: string
}

// Monotone cubic interpolation (Fritsch–Carlson): never overshoots the data, so
// a smoothed line cannot invent a peak the samples do not contain.
export function monotonePath(points: Array<[number, number]>): string {
  const n = points.length
  if (n === 0) return ''
  if (n === 1) return `M${points[0][0].toFixed(2)} ${points[0][1].toFixed(2)}`
  const dx: number[] = []
  const slope: number[] = []
  for (let i = 0; i < n - 1; i++) {
    dx[i] = points[i + 1][0] - points[i][0]
    slope[i] = (points[i + 1][1] - points[i][1]) / (dx[i] || 1e-9)
  }
  const tangent = new Array<number>(n)
  tangent[0] = slope[0]
  tangent[n - 1] = slope[n - 2]
  for (let i = 1; i < n - 1; i++) {
    tangent[i] = slope[i - 1] * slope[i] <= 0 ? 0 : (slope[i - 1] + slope[i]) / 2
  }
  for (let i = 0; i < n - 1; i++) {
    if (slope[i] === 0) {
      tangent[i] = 0
      tangent[i + 1] = 0
      continue
    }
    const a = tangent[i] / slope[i]
    const b = tangent[i + 1] / slope[i]
    const s = a * a + b * b
    if (s > 9) {
      const scale = 3 / Math.sqrt(s)
      tangent[i] = scale * a * slope[i]
      tangent[i + 1] = scale * b * slope[i]
    }
  }
  let d = `M${points[0][0].toFixed(2)} ${points[0][1].toFixed(2)}`
  for (let i = 0; i < n - 1; i++) {
    const h = dx[i] / 3
    d += `C${(points[i][0] + h).toFixed(2)} ${(points[i][1] + tangent[i] * h).toFixed(2)} ` +
      `${(points[i + 1][0] - h).toFixed(2)} ${(points[i + 1][1] - tangent[i + 1] * h).toFixed(2)} ` +
      `${points[i + 1][0].toFixed(2)} ${points[i + 1][1].toFixed(2)}`
  }
  return d
}

// Split the series into unbroken runs so a NaN leaves a gap in the line.
function runs(values: number[]): number[][] {
  const out: number[][] = []
  let run: number[] = []
  for (const value of values) {
    if (Number.isFinite(value)) run.push(value)
    else if (run.length) { out.push(run); run = [] }
  }
  if (run.length) out.push(run)
  return out
}

export default function Sparkline({
  values,
  color = 'var(--accent-brand, currentColor)',
  height = 34,
  labels,
  format = (value) => value.toLocaleString('en-US'),
  ariaLabel,
}: SparklineProps) {
  const reduced = useReducedMotion() ?? false
  const gradientId = useId().replace(/:/g, '')
  const boxRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(220)
  const [active, setActive] = useState<number | null>(null)

  useLayoutEffect(() => {
    const element = boxRef.current
    if (!element) return
    const update = () => setWidth(Math.max(40, Math.round(element.clientWidth)))
    update()
    const observer = new ResizeObserver(update)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  const finite = values.filter((value) => Number.isFinite(value))
  if (finite.length < 2) {
    // One sample (or none) has no shape; the tile's own numeric value carries it.
    return null
  }

  const padX = 1
  const padTop = 4
  const padBottom = 3
  const plotHeight = Math.max(12, height - padTop - padBottom)
  const min = Math.min(...finite)
  const max = Math.max(...finite)
  const span = max - min || 1
  const stepX = values.length > 1 ? (width - padX * 2) / (values.length - 1) : 0
  const x = (index: number) => padX + index * stepX
  const y = (value: number) => padTop + (1 - (value - min) / span) * plotHeight

  const paths = runs(values).map((run) => {
    const offset = values.indexOf(run[0])
    const points = run.map((value, index) => [x(offset + index), y(value)] as [number, number])
    return {
      line: monotonePath(points),
      area: `${monotonePath(points)}L${points[points.length - 1][0].toFixed(2)} ${(padTop + plotHeight).toFixed(2)}L${points[0][0].toFixed(2)} ${(padTop + plotHeight).toFixed(2)}Z`,
      points,
      offset,
    }
  })

  const lastIndex = values.length - 1
  const lastValue = values[lastIndex]
  const target = active ?? lastIndex

  const onPointer = (event: React.PointerEvent<HTMLDivElement>) => {
    const element = boxRef.current
    if (!element) return
    const box = element.getBoundingClientRect()
    const index = Math.round((event.clientX - box.left - padX) / (stepX || 1))
    setActive(Math.min(Math.max(index, 0), lastIndex))
  }
  const onKey = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'ArrowRight') setActive(Math.min((active ?? lastIndex - 1) + 1, lastIndex))
    else if (event.key === 'ArrowLeft') setActive(Math.max((active ?? lastIndex + 1) - 1, 0))
    else if (event.key === 'Escape') setActive(null)
    else return
    event.preventDefault()
  }

  const readout = Number.isFinite(values[target]) ? format(values[target]) : 'No sample'
  const caption = labels?.[target] ?? `Point ${target + 1} of ${values.length}`

  return (
    <div
      ref={boxRef}
      className="sparkline"
      tabIndex={0}
      role="img"
      aria-label={ariaLabel ?? `Trend of ${values.length} samples, from ${format(finite[0])} to ${format(finite[finite.length - 1])}`}
      onPointerMove={onPointer}
      onPointerLeave={() => setActive(null)}
      onKeyDown={onKey}
      onBlur={() => setActive(null)}
      style={{ height }}
    >
      <svg width={width} height={height} aria-hidden="true">
        <defs>
          <linearGradient id={`${gradientId}-wash`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor={color} stopOpacity="0.22" />
            <stop offset="1" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>
        {paths.map((path) => (
          <g key={`${path.offset}-${path.points.length}`}>
            <motion.path
              d={path.area}
              fill={`url(#${gradientId}-wash)`}
              initial={reduced ? false : { opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={reduced ? { duration: 0 } : { duration: 0.5, ease: [0.23, 1, 0.32, 1] }}
            />
            <motion.path
              d={path.line}
              fill="none"
              stroke={color}
              strokeWidth={1.6}
              strokeLinecap="round"
              strokeLinejoin="round"
              initial={reduced ? false : { pathLength: 0 }}
              animate={{ pathLength: 1 }}
              transition={reduced ? { duration: 0 } : { duration: 0.7, ease: [0.23, 1, 0.32, 1] }}
            />
          </g>
        ))}
        {/* Latest sample: one dot, never a dot per point. */}
        {Number.isFinite(lastValue) && (
          <circle cx={x(lastIndex)} cy={y(lastValue)} r={2.6} fill={color} />
        )}
        {active !== null && Number.isFinite(values[active]) && (
          <g>
            <line
              x1={x(active)} y1={padTop} x2={x(active)} y2={padTop + plotHeight}
              stroke="var(--chart-grid-color, rgba(10,10,10,.2))" strokeWidth={1}
            />
            <circle cx={x(active)} cy={y(values[active])} r={3} fill={color} stroke="var(--card)" strokeWidth={1.5} />
          </g>
        )}
      </svg>
      <span className="visually-hidden" role="status" aria-live="polite">
        {active === null ? '' : `${caption}: ${readout}`}
      </span>
    </div>
  )
}
