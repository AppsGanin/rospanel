// Chart wrappers over recharts (replaces @mantine/charts).
import {
  Area,
  AreaChart as RAreaChart,
  CartesianGrid,
  Cell,
  Legend,
  Pie,
  PieChart as RPieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { useEffect, useRef, useState } from 'react'
import i18n from './i18n'
import { cn, Mono, useWideBox } from './ui'

// seriesLabel names the two traffic series. recharts hands the formatter the raw
// dataKey, so this maps it rather than the component threading labels down.
function seriesLabel(key: unknown): string {
  return key === 'down' ? i18n.t('traffic.received') : i18n.t('traffic.sent')
}

// recharts 3 widened the tooltip formatter's value to `ValueType | undefined`
// (it can be a string or an array for other chart kinds). Every series we plot is
// numeric, so narrow once here instead of asserting at each call site.
function num(v: unknown): number {
  return typeof v === 'number' ? v : Number(v ?? 0)
}

// Read a themed CSS variable at render time so charts follow the colour theme.
// Falls back to the stock value before styles resolve.
export function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

const TEAL = '#0d9488' // distinct second data series (kept independent of accent)

type Point = { day: string; up: number; down: number }

export function TrafficArea({
  data,
  height = 260,
  fmt,
}: {
  data: Point[]
  height?: number
  fmt: (n: number) => string
}) {
  const blue = cssVar('--color-brand-600', '#0d4cd3')
  const grid = cssVar('--color-gray-200', '#eef2f7')
  const axis = cssVar('--color-ink-muted', '#8995a5')
  return (
    <ResponsiveContainer width="100%" height={height}>
      <RAreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id="gDown" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={TEAL} stopOpacity={0.35} />
            <stop offset="100%" stopColor={TEAL} stopOpacity={0} />
          </linearGradient>
          <linearGradient id="gUp" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={blue} stopOpacity={0.35} />
            <stop offset="100%" stopColor={blue} stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke={grid} vertical={false} />
        <XAxis dataKey="day" tick={{ fontSize: 12, fill: axis }} tickLine={false} axisLine={false} />
        <YAxis tickFormatter={fmt} tick={{ fontSize: 11, fill: axis }} tickLine={false} axisLine={false} width={56} />
        <Tooltip
          formatter={(v, n) => [fmt(num(v)), seriesLabel(n)]}
          contentStyle={{ borderRadius: 12, border: `1px solid ${grid}`, fontSize: 13 }}
        />
        <Legend
          formatter={(v) => seriesLabel(v)}
          iconType="circle"
          wrapperStyle={{ fontSize: 13 }}
        />
        <Area type="monotone" dataKey="down" stroke={TEAL} fill="url(#gDown)" strokeWidth={2} />
        <Area type="monotone" dataKey="up" stroke={blue} fill="url(#gUp)" strokeWidth={2} />
      </RAreaChart>
    </ResponsiveContainer>
  )
}

export function TrafficDonut({
  data,
  size = 240,
  fmt,
  centerLabel,
}: {
  data: { name: string; value: number; color: string }[]
  size?: number
  fmt: (n: number) => string
  centerLabel?: string
}) {
  const grid = cssVar('--color-gray-200', '#eef2f7')
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <ResponsiveContainer width="100%" height="100%">
        <RPieChart>
          <Tooltip
            formatter={(v, n) => [fmt(num(v)), n]}
            contentStyle={{ borderRadius: 12, border: `1px solid ${grid}`, fontSize: 13 }}
          />
          <Pie
            data={data}
            dataKey="value"
            nameKey="name"
            innerRadius="62%"
            outerRadius="100%"
            paddingAngle={1}
            stroke="none"
          >
            {data.map((d, i) => (
              <Cell key={i} fill={d.color} />
            ))}
          </Pie>
        </RPieChart>
      </ResponsiveContainer>
      {centerLabel && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm font-semibold text-ink">
          {centerLabel}
        </div>
      )}
    </div>
  )
}

/* --------------------------------------------------------- plain-DOM charts */
// The statistics screen draws its own bars rather than reaching for recharts: a
// share is a rectangle, and a rectangle built from the panel's own tokens follows
// the operator's branding, needs no measuring pass, and reads at any width.

// ShareBar is the one row shape used for "who/where/which server carried how much":
// a name, the share as a bar against the largest row, and the figure in mono.
export function ShareBar({
  label,
  glyph,
  percent,
  value,
  title,
}: {
  label: string
  // A leading flag or icon where the row has one (countries); omitted elsewhere.
  glyph?: string
  percent: number
  value: string
  title?: string
}) {
  const p = Math.max(0, Math.min(100, percent || 0))
  return (
    <div className="flex items-center gap-2.5" title={title}>
      {glyph && (
        <span className="w-5 shrink-0 text-center text-sm leading-none">{glyph}</span>
      )}
      <span className="w-24 shrink-0 truncate text-xs text-ink sm:w-28">{label}</span>
      <span className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-gray-200">
        <span
          className="block h-full rounded-full bg-brand-600"
          style={{ width: `${p}%`, minWidth: p > 0 ? 2 : 0 }}
        />
      </span>
      <Mono className="w-20 shrink-0 text-right text-[11px] text-ink-muted">
        {value}
      </Mono>
    </div>
  )
}

// dm renders a day key as DD.MM — the axis label, and the unit the tooltip counts in.
function dm(day: string): string {
  return `${day.slice(8, 10)}.${day.slice(5, 7)}`
}

type Day = { day: string; value: number; today?: boolean }
export type Bar = { key: string; label: string; title: string; value: number; today: boolean }

// bucketize keeps the number of columns readable whatever the period is. A month of
// days fits as days; a quarter is read by weeks; a year by months. Without this a
// 365-day range asks for 365 columns, the gaps alone are wider than the panel, and
// every bar is squeezed to nothing — which is exactly what it looked like.
// MIN_COL is the narrowest a column may get before the chart stops being a chart:
// thirty days across a phone is 11px a column, which reads as a comb. COL_GAP is the
// space between two columns (gap-1), counted in so the floor is the width of the bar
// itself rather than of the bar plus its gap.
const MIN_COL = 26
const COL_GAP = 4
// LABEL_W is what one date needs: "03.09" is 30px at 10px mono, plus the air that
// keeps it from touching the next one. Dates thin out to fit this, which is not the
// same rule as the columns' — three columns can share one date, and on a phone they
// often do.
const LABEL_W = 38

// merge folds neighbouring columns together until they fit, so a period the panel
// can show as days on a desktop is shown as pairs or triples on a phone rather than
// as slivers. Grouped from the end, like the weekly buckets: the newest column is
// whole, and any remainder lands at the far left where it is oldest.
function merge(bars: Bar[], maxBars: number): Bar[] {
  if (maxBars < 1 || bars.length <= maxBars) return bars
  const size = Math.ceil(bars.length / maxBars)
  const out: Bar[] = []
  for (let end = bars.length; end > 0; end -= size) {
    const chunk = bars.slice(Math.max(0, end - size), end)
    const first = chunk[0]
    const last = chunk[chunk.length - 1]
    out.unshift({
      key: first.key,
      label: first.label,
      title: chunk.length === 1 ? first.title : `${first.label} — ${last.label}`,
      value: chunk.reduce((a, b) => a + b.value, 0),
      today: chunk.some((b) => b.today),
    })
  }
  return out
}

function bucketize(data: Day[]): Bar[] {
  const one = (d: Day): Bar => ({
    key: d.day,
    label: dm(d.day),
    title: dm(d.day),
    value: d.value,
    today: !!d.today,
  })
  if (data.length <= 31) return data.map(one)

  if (data.length <= 120) {
    // Chunked from the end, so the newest bucket is the week that ends today rather
    // than a partial week left over at the front.
    const out: Bar[] = []
    for (let end = data.length; end > 0; end -= 7) {
      const chunk = data.slice(Math.max(0, end - 7), end)
      out.unshift({
        key: chunk[0].day,
        label: dm(chunk[0].day),
        title: `${dm(chunk[0].day)} — ${dm(chunk[chunk.length - 1].day)}`,
        value: chunk.reduce((a, d) => a + d.value, 0),
        today: chunk.some((d) => d.today),
      })
    }
    return out
  }

  const months = new Map<string, Bar>()
  for (const d of data) {
    const k = d.day.slice(0, 7) // YYYY-MM
    const b = months.get(k)
    if (b) {
      b.value += d.value
      b.today = b.today || !!d.today
    } else {
      months.set(k, {
        key: k,
        label: `${k.slice(5, 7)}.${k.slice(2, 4)}`,
        title: k,
        value: d.value,
        today: !!d.today,
      })
    }
  }
  return [...months.values()]
}


// DayBars is the traffic-per-period column chart: grey columns, heights taken from
// the largest one, and the accent reserved for the one the reader picked — today is
// already named under the last column and does not need a second marker. Dates thin out as the period
// grows — they cannot be read side by side past a handful — and the figure is not
// printed over the columns at all: a month of them is 29px wide each, and no type
// size fits "412.6 ГБ" there. Tap a column and it says its own span and figure.
export function DayBars({
  data,
  fmt,
  onHover,
}: {
  data: Day[]
  fmt: (n: number) => string
  // Which column the pointer is on, so the panel around the chart can print that
  // day's figure where the total normally sits. A chart of fourteen bars is read by
  // pointing at one of them.
  onHover?: (bar: Bar | null) => void
}) {
  // The box decides how many columns are worth drawing: same data, fewer and wider
  // columns on a phone. Measured, not guessed from a breakpoint — this chart sits in
  // a panel, a drawer and a dashboard tile, each a different width at the same
  // viewport.
  const [measureRef, , boxW] = useWideBox(0)
  const bars = merge(
    bucketize(data),
    boxW > 0 ? Math.floor((boxW + COL_GAP) / (MIN_COL + COL_GAP)) : 999,
  )
  const max = bars.reduce((a, d) => Math.max(a, d.value), 0)
  const every = Math.max(
    1,
    Math.ceil(bars.length / Math.max(1, Math.floor(boxW / LABEL_W) || bars.length)),
  )
  // One or two columns stretched across the panel read as a slab, not as a chart:
  // few of them keep a column's width and sit in the middle.
  const few = bars.length <= 3
  // Which column was tapped. A hover title is nothing on a touch screen, and on a
  // dense chart the figure lives nowhere else — so a tap puts it on screen and a
  // second tap (or a tap on another column) takes it away.
  const [picked, setPicked] = useState<string | null>(null)
  // One element, two jobs: it is measured (a callback ref) and it is asked whether a
  // click landed inside it (a node), so the two refs are joined here.
  const boxEl = useRef<HTMLDivElement | null>(null)
  const setBox = (el: HTMLDivElement | null) => {
    boxEl.current = el
    measureRef(el)
  }
  // A tapped column belongs to the data that was on screen when it was tapped. Switch
  // the range and the same key can exist in the new set — so the tooltip would hang
  // over a different chart, quoting a figure nobody asked for.
  const sig = `${bars.length}:${bars[0]?.key ?? ''}:${bars[bars.length - 1]?.key ?? ''}`
  useEffect(() => setPicked(null), [sig])
  // A tooltip opened by a tap is closed by a tap anywhere else — the chart is not a
  // dialog, and a figure that follows the reader around the page is noise.
  useEffect(() => {
    if (!picked) return
    const away = (e: MouseEvent) => {
      if (!boxEl.current?.contains(e.target as Node)) setPicked(null)
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [picked])
  return (
    <div
      ref={setBox}
      className={cn(
        'flex items-end',
        few && 'justify-center',
        bars.length > 31 ? 'gap-px' : 'gap-1 sm:gap-2',
      )}
    >
      {bars.map((d, i) => {
        const pct = max > 0 ? (d.value / max) * 100 : 0
        const open = picked === d.key
        return (
          <span
            key={d.key}
            className={cn(
              'group relative flex min-w-0 flex-1 cursor-pointer flex-col items-center gap-1.5',
              few && 'max-w-24',
            )}
            title={`${d.title} · ${fmt(d.value)}`}
            onClick={() => setPicked((p) => (p === d.key ? null : d.key))}
            onMouseEnter={onHover ? () => onHover(d) : undefined}
            onMouseLeave={onHover ? () => onHover(null) : undefined}
          >
            {open && (
              <span className="absolute bottom-full left-1/2 z-10 mb-1 -translate-x-1/2 whitespace-nowrap rounded-md bg-brand-600 px-2 py-1 text-[11px] font-medium text-onaccent shadow-lg">
                {d.title} · {fmt(d.value)}
              </span>
            )}
            <span className="flex h-24 w-full items-end">
              <span
                className={cn(
                  'w-full rounded-t-md transition-colors',
                  open ? 'bg-brand-600' : 'bg-gray-400 group-hover:bg-brand-600',
                )}
                style={{ height: `${pct}%`, minHeight: d.value > 0 ? 2 : 0 }}
              />
            </span>
            <Mono className="truncate text-[10px] text-ink-muted">
              {i % every === 0 ? d.label : '\u00a0'}
            </Mono>
          </span>
        )
      })}
    </div>
  )
}
