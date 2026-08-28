import i18n from './i18n'

// The option tables below are functions, not constants: a constant would freeze
// the labels in whichever language happened to be active when the module was first
// imported, and switching language would leave every dropdown behind.
export function fmtBytes(n: number): string {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`
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

// fmtDuration renders a span of seconds compactly: "1d 13h", "6h 4m", "12m".
export function fmtDuration(sec: number): string {
  if (sec <= 0) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${Math.floor(sec)}s`
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
