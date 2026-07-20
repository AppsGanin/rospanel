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
export const QUOTA_OPTIONS = [
  { value: '0', label: 'Без лимита' },
  { value: '0.09765625', label: '100 МБ' },
  { value: '0.48828125', label: '500 МБ' },
  { value: '1', label: '1 ГБ' },
  { value: '5', label: '5 ГБ' },
  { value: '10', label: '10 ГБ' },
  { value: '25', label: '25 ГБ' },
  { value: '50', label: '50 ГБ' },
  { value: '100', label: '100 ГБ' },
  { value: '250', label: '250 ГБ' },
  { value: '500', label: '500 ГБ' },
]

// Per-user simultaneous device cap options ("0" = unlimited), used by the user
// detail editor.
export const DEVICE_LIMIT_OPTIONS = [
  { value: '0', label: 'Без лимита' },
  { value: '1', label: '1 устройство' },
  { value: '2', label: '2 устройства' },
  { value: '3', label: '3 устройства' },
  { value: '5', label: '5 устройств' },
  { value: '10', label: '10 устройств' },
]

// Automatic quota-reset period options, shared by the create form and the user
// detail editor.
export const RESET_PERIODS = [
  { value: 'none', label: 'Без автосброса' },
  { value: 'daily', label: 'Ежедневно' },
  { value: 'weekly', label: 'Еженедельно' },
  { value: 'monthly', label: 'Ежемесячно' },
  { value: 'yearly', label: 'Ежегодно' },
]

export function gbToBytes(gb: number): number {
  return Math.round(gb * GB)
}

// Date-range options for the traffic segmented controls, shared by the stats
// panel and the per-user detail drawer. A year is the widest option on purpose:
// the server keeps per-day traffic for model.TrafficDailyRetentionDays (365) and
// sweeps the rest, so an "all time" button would only ever return the same rows as
// "Год" — while promising history that no longer exists.
export const RANGES = [
  { value: '1', label: 'День' },
  { value: '7', label: '7д' },
  { value: '30', label: '30д' },
  { value: '90', label: '90д' },
  { value: '365', label: 'Год' },
]

// plural picks the Russian form for n: one / few / many.
export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m100 >= 11 && m100 <= 14) return many
  if (m10 === 1) return one
  if (m10 >= 2 && m10 <= 4) return few
  return many
}

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
      return { label: 'активно', color: 'teal' }
    case 'disabled':
      return { label: 'отключено', color: 'gray' }
    case 'expired':
      return { label: 'срок истёк', color: 'red' }
    case 'limited':
      return { label: 'лимит исчерпан', color: 'orange' }
    case 'device_limited':
      return { label: 'лимит устройств', color: 'orange' }
    default:
      return { label: status, color: 'gray' }
  }
}

// online if activity within the last 2 minutes (poller runs every 60s).
export function isOnline(lastSeen: number): boolean {
  return lastSeen > 0 && Date.now() / 1000 - lastSeen < 120
}

export function fmtLastSeen(unix: number): string {
  if (!unix) return 'не подключался'
  const sec = Math.floor(Date.now() / 1000 - unix)
  if (sec < 120) return 'только что'
  if (sec < 3600) return `${Math.floor(sec / 60)} мин назад`
  if (sec < 86400) return `${Math.floor(sec / 3600)} ч назад`
  if (sec < 7 * 86400) return `${Math.floor(sec / 86400)} дн назад`
  return new Date(unix * 1000).toLocaleString()
}
