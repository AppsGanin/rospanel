import i18n from './i18n'

// The option tables below are functions, not constants: a constant would freeze
// the labels in whichever language happened to be active when the module was first
// imported, and switching language would leave every dropdown behind.
// Units come from the dictionary, not from a constant: a Russian panel says
// "412.6 ГБ", and a byte figure appears on nearly every screen, so leaving it in
// English is the most visible way for the interface to be half-translated.
// Every "when did this happen" figure in the panel reads the same way: numeric,
// fixed width, in mono — 13.07.2026, 15:48. A month name ("12 июл. 2026 г.") is a
// different width in every row and cannot be scanned down a column.
export const STAMP_OPTS: Intl.DateTimeFormatOptions = {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
}

// fmtStamp takes unix SECONDS; 0 means "never happened" and reads as a dash.
export function fmtStamp(unix: number): string {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString(i18n.language, STAMP_OPTS)
}

const BYTE_UNITS = ['b', 'kb', 'mb', 'gb', 'tb'] as const

export function fmtBytes(n: number): string {
  const unit = (i: number) => i18n.t(`bytes.${BYTE_UNITS[i]}` as 'bytes.b')
  if (!n) return `0 ${unit(0)}`
  let i = 0
  let v = n
  while (v >= 1024 && i < BYTE_UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${unit(i)}`
}

const GB = 1024 * 1024 * 1024

// Preset traffic-limit options (GiB as string; "0" = unlimited) shared by the
// create form, the user detail editor and the tariff-plan editor — one source of
// truth so every place offers the same values. The two sub-GiB presets use exact
// GiB fractions (100/1024, 500/1024) so gbToBytes round-trips to whole MiB.
export const quotaOptions = () => [
  { value: '0', label: i18n.t('quota.unlimited') },
  { value: '0.09765625', label: i18n.t('quota.mb', { n: 100 }) },
  { value: '0.48828125', label: i18n.t('quota.mb', { n: 500 }) },
  { value: '1', label: i18n.t('quota.gb', { n: 1 }) },
  { value: '5', label: i18n.t('quota.gb', { n: 5 }) },
  { value: '10', label: i18n.t('quota.gb', { n: 10 }) },
  { value: '25', label: i18n.t('quota.gb', { n: 25 }) },
  { value: '50', label: i18n.t('quota.gb', { n: 50 }) },
  { value: '100', label: i18n.t('quota.gb', { n: 100 }) },
  { value: '250', label: i18n.t('quota.gb', { n: 250 }) },
  { value: '500', label: i18n.t('quota.gb', { n: 500 }) },
]

// Per-user simultaneous device cap options ("0" = unlimited), used by the user
// detail editor.
export const deviceLimitOptions = () => [
  { value: '0', label: i18n.t('devices.unlimited') },
  ...[1, 2, 3, 5, 10].map((n) => ({
    value: String(n),
    label: i18n.t('devices.count', { count: n }),
  })),
]

// Per-user speed caps, in kbit/s — the unit the server stores, so nothing has to be
// converted on the way in or out. The labels are in Mbit/s because that is how the
// speeds are sold; the one sub-megabit step exists because "512 Kbps" plans do too.
export const speedLimitOptions = () => [
  { value: '0', label: i18n.t('speed.unlimited') },
  { value: '512', label: i18n.t('speed.kbit', { n: 512 }) },
  ...[1, 2, 5, 10, 20, 50, 100, 200].map((n) => ({
    value: String(n * 1000),
    label: i18n.t('speed.mbit', { n }),
  })),
]

// fmtSpeed renders a stored kbit/s cap the way the options above label it, for a
// value that isn't one of the presets (set through the API, say).
export const fmtSpeed = (kbps: number): string => {
  if (kbps <= 0) return i18n.t('speed.unlimited')
  if (kbps < 1000) return i18n.t('speed.kbit', { n: kbps })
  return i18n.t('speed.mbit', { n: Math.round((kbps / 1000) * 10) / 10 })
}

// Automatic quota-reset period options, shared by the create form and the user
// detail editor.
export const resetPeriods = () => [
  { value: 'none', label: i18n.t('reset.none') },
  { value: 'daily', label: i18n.t('reset.daily') },
  { value: 'weekly', label: i18n.t('reset.weekly') },
  { value: 'monthly', label: i18n.t('reset.monthly') },
  { value: 'yearly', label: i18n.t('reset.yearly') },
]

export function gbToBytes(gb: number): number {
  return Math.round(gb * GB)
}

// Date-range options for the traffic segmented controls, shared by the stats
// panel and the per-user detail drawer. A year is the widest option on purpose:
// the server keeps per-day traffic for model.TrafficDailyRetentionDays (365) and
// sweeps the rest, so an "all time" button would only ever return the same rows as
// the "Year" button — while promising history that no longer exists.
export const ranges = () => [
  { value: '1', label: i18n.t('range.day') },
  { value: '7', label: i18n.t('range.d7') },
  { value: '30', label: i18n.t('range.d30') },
  { value: '90', label: i18n.t('range.d90') },
  { value: '365', label: i18n.t('range.year') },
]

// fmtDuration renders a span of seconds compactly, in the reader's language:
// "6 д 4 ч", "6 h 4 m", "12 м". Two units at most — this is read at a glance next to
// a figure, not used to settle a stopwatch.
export function fmtDuration(sec: number): string {
  if (sec <= 0) return '—'
  const u = (k: 'd' | 'h' | 'm' | 's', n: number) => i18n.t(`dur.${k}` as 'dur.d', { n })
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${u('d', d)} ${u('h', h)}`
  if (h > 0) return `${u('h', h)} ${u('m', m)}`
  if (m > 0) return u('m', m)
  return u('s', Math.floor(sec))
}

// localDay returns the calendar day (YYYY-MM-DD) in the browser's local time,
// `offset` days back from today. Uses local time (not UTC) so day boundaries
// match the operator's day, consistent with the server's local-day buckets.
export function localDay(offset: number): string {
  const d = new Date(Date.now() - offset * 86400000)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

export function fmtExpire(unix: number): string {
  if (!unix) return '∞'
  const d = new Date(unix * 1000)
  return d.toLocaleDateString()
}

export function fmtQuota(used: number, limit: number): string {
  if (!limit) return fmtBytes(used)
  return `${fmtBytes(used)} / ${fmtBytes(limit)}`
}

export function statusInfo(status: string): { label: string; color: string } {
  switch (status) {
    case 'active':
      return { label: i18n.t('status.active'), color: 'teal' }
    case 'disabled':
      return { label: i18n.t('status.disabled'), color: 'gray' }
    case 'expired':
      return { label: i18n.t('status.expired'), color: 'red' }
    case 'limited':
      return { label: i18n.t('status.limited'), color: 'orange' }
    case 'device_limited':
      return { label: i18n.t('status.deviceLimited'), color: 'orange' }
    default:
      return { label: status, color: 'gray' }
  }
}

// online if activity within the last 2 minutes (poller runs every 60s).
export function isOnline(lastSeen: number): boolean {
  return lastSeen > 0 && Date.now() / 1000 - lastSeen < 120
}

export function fmtLastSeen(unix: number): string {
  if (!unix) return i18n.t('lastSeen.never')
  const sec = Math.floor(Date.now() / 1000 - unix)
  if (sec < 120) return i18n.t('lastSeen.justNow')
  if (sec < 3600) return i18n.t('lastSeen.minutes', { n: Math.floor(sec / 60) })
  if (sec < 86400) return i18n.t('lastSeen.hours', { n: Math.floor(sec / 3600) })
  if (sec < 7 * 86400) return i18n.t('lastSeen.days', { n: Math.floor(sec / 86400) })
  return new Date(unix * 1000).toLocaleString()
}

// countryFlag turns a 2-letter country code into its emoji flag via regional-indicator
// symbols; anything else (the "" unknown bucket) gets a globe. Shared because the map
// and the scanner list both name countries and must name them the same way.
export function countryFlag(code: string): string {
  if (code.length !== 2) return '\u{1F310}'
  const base = 0x1f1e6
  const up = code.toUpperCase()
  return String.fromCodePoint(
    base + up.charCodeAt(0) - 65,
    base + up.charCodeAt(1) - 65,
  )
}

// countryName resolves a 2-letter code to its name in the reader's language, falling
// back to the upper-cased code. Shared so the map and the scanner list agree.
export function countryName(code: string, lang: string, unknown: string): string {
  if (code.length !== 2) return unknown
  try {
    const dn = new Intl.DisplayNames([lang], { type: 'region' })
    return dn.of(code.toUpperCase()) || code.toUpperCase()
  } catch {
    return code.toUpperCase()
  }
}

// Expiry dates are picked from a calendar and stored as a unix second, and the naive
// conversion is wrong twice over.
//
// `new Date("2026-09-10")` is parsed as UTC midnight, so an operator selling access
// "until the 10th" cuts the customer off at the START of the 10th — a day early as far
// as the customer is concerned. And `toISOString().slice(0,10)` renders a stored moment
// as its UTC calendar day, so anyone west of UTC reads their own saved date back as the
// day before.
//
// Both helpers work in the browser's local calendar, which is the one the picker shows.

// dateToUnixEndOfDay turns a "YYYY-MM-DD" from a date input into the last second of
// that day, locally. An empty string means no expiry, which is 0.
export function dateToUnixEndOfDay(date: string): number {
  const [y, m, d] = date.split('-').map(Number)
  if (!y || !m || !d) return 0
  return Math.floor(new Date(y, m - 1, d, 23, 59, 59).getTime() / 1000)
}

// unixToLocalDate renders a stored expiry as the "YYYY-MM-DD" a date input wants, in
// the reader's own calendar.
export function unixToLocalDate(unix: number): string {
  if (!unix) return ''
  const d = new Date(unix * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
