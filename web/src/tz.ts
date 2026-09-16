// IANA timezone helpers: the zone lists the wizard and the settings page offer, and the
// panel's own zone every date in the panel is shown in.

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

function tzList(def: string): string[] {
  const sov = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
  let zones: string[] = []
  try {
    zones = sov ? sov('timeZone') : []
  } catch {
    zones = []
  }
  if (zones.length === 0) {
    zones = ['UTC', 'Europe/Moscow', 'Europe/Kaliningrad', 'Asia/Yekaterinburg', def]
  }
  if (!zones.includes(def)) zones = [def, ...zones]
  return Array.from(new Set(zones))
}

// tzOffset returns the current UTC offset of a zone as "+3" / "-5" / "+5:30".
export function tzOffset(tz: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      timeZoneName: 'shortOffset',
    }).formatToParts(new Date())
    const name = parts.find((p) => p.type === 'timeZoneName')?.value ?? ''
    const m = name.match(/GMT([+-]\d{1,2}(?::\d{2})?)?/)
    return m && m[1] ? m[1] : '+0'
  } catch {
    return ''
  }
}

// tzOptions builds Select data with offset labels, e.g. "Europe/Moscow (UTC+3)".
export function tzOptions(def: string): { value: string; label: string }[] {
  return tzList(def).map((z) => ({ value: z, label: `${z} (UTC${tzOffset(z)})` }))
}

// ---------------------------------------------------------------- the panel's zone
//
// Every date and time the panel shows is read in the operator's timezone (General
// settings), the one its statistics, logs and backup schedules already keep. The
// browser's zone is only the default until the panel has said which one it is: an
// admin whose laptop sits in another zone used to see the clock and every date in
// their own, beside logs and day totals in the panel's.

let panelTz = ''
// epoch moves when the zone dates are shown in changes once the panel has said which
// one it is, so the app can redraw every date it has already shown (see App). The
// first word draws nothing twice: nothing is on screen yet.
let epoch = 0
let told = false
const listeners = new Set<() => void>()

export function isValidTimezone(tz: string): boolean {
  if (!tz) return false
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz })
    return true
  } catch {
    return false
  }
}

// setPanelTimezone records the panel's zone. One the browser does not know is ignored
// and the browser's own is used, as before the panel said anything.
export function setPanelTimezone(tz: string): void {
  const next = isValidTimezone(tz) ? tz : ''
  const before = panelTimezone()
  const first = !told
  told = true
  if (next === panelTz) return
  panelTz = next
  if (panelTimezone() === before) return
  if (!first) epoch++
  for (const l of listeners) l()
}

export function panelTimezone(): string {
  return panelTz || browserTimezone()
}

export function panelTimezoneEpoch(): number {
  return epoch
}

export function subscribePanelTimezone(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

// inPanelTz is a date format for an instant, read in the panel's zone. Not for a
// calendar date the picker holds as a local midnight: shown in another zone, that
// midnight can land on the day before.
export function inPanelTz(opts?: Intl.DateTimeFormatOptions): Intl.DateTimeFormatOptions {
  return { ...opts, timeZone: panelTimezone() }
}

type Parts = { y: number; mo: number; d: number; h: number; mi: number; s: number }

const partFormatters = new Map<string, Intl.DateTimeFormat>()

// zonedParts is the wall-clock date and time of an instant in a zone.
export function zonedParts(ms: number, tz: string = panelTimezone()): Parts {
  let f = partFormatters.get(tz)
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      hourCycle: 'h23',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      second: 'numeric',
    })
    partFormatters.set(tz, f)
  }
  const out: Parts = { y: 0, mo: 0, d: 0, h: 0, mi: 0, s: 0 }
  for (const p of f.formatToParts(new Date(ms))) {
    const n = Number(p.value)
    if (p.type === 'year') out.y = n
    else if (p.type === 'month') out.mo = n
    else if (p.type === 'day') out.d = n
    else if (p.type === 'hour') out.h = n % 24 // an engine that still says 24 for midnight
    else if (p.type === 'minute') out.mi = n
    else if (p.type === 'second') out.s = n
  }
  return out
}

// offsetMs is how far a zone's wall clock is ahead of UTC at an instant.
function offsetMs(ms: number, tz: string): number {
  const p = zonedParts(ms, tz)
  return Date.UTC(p.y, p.mo - 1, p.d, p.h, p.mi, p.s) - Math.floor(ms / 1000) * 1000
}

const DAY_MS = 86400000

// zonedToMs is the instant a zone's wall clock shows the given date and time. A wall
// time the clocks pass twice (the hour repeated when they go back) is its first
// passing; one they skip (the hour lost when they go forward) moves forward by what
// was skipped, 02:30 to 03:30 — so 00:00 on a day whose clocks jump from 00:00 to
// 01:00 is 01:00, that day's first instant, not the last hour of the day before.
export function zonedToMs(y: number, mo: number, d: number, h: number, mi: number, s: number, tz: string = panelTimezone()): number {
  const wall = Date.UTC(y, mo - 1, d, h, mi, s)
  // The offsets a day either side: two changes of the clocks two days apart do not
  // happen anywhere.
  const before = offsetMs(wall - DAY_MS, tz)
  const after = offsetMs(wall + DAY_MS, tz)
  const hi = Math.max(before, after)
  const lo = Math.min(before, after)
  if (offsetMs(wall - hi, tz) === hi) return wall - hi
  if (offsetMs(wall - lo, tz) === lo) return wall - lo
  return wall - before
}

// dayStartMs is the first instant of a calendar date in a zone.
export function dayStartMs(y: number, mo: number, d: number, tz: string = panelTimezone()): number {
  return zonedToMs(y, mo, d, 0, 0, 0, tz)
}

// dayEndMs is the last whole second of a calendar date in a zone: a second before the
// next date starts. Not 23:59:59 read off the clock, which a day whose clocks go back
// at midnight passes twice — the first time an hour before the day is over.
export function dayEndMs(y: number, mo: number, d: number, tz: string = panelTimezone()): number {
  return dayStartMs(y, mo, d + 1, tz) - 1000
}

const pad = (n: number) => String(n).padStart(2, '0')

// ymdOf is the calendar date (YYYY-MM-DD) an instant falls on in a zone.
export function ymdOf(ms: number, tz: string = panelTimezone()): string {
  const p = zonedParts(ms, tz)
  return `${p.y}-${pad(p.mo)}-${pad(p.d)}`
}

// todayYmd is the calendar date in a zone, `offsetDays` days back from today. The day
// arithmetic is done on the date alone, so a day that is 23 or 25 hours long moves by
// exactly one.
export function todayYmd(offsetDays = 0, tz: string = panelTimezone()): string {
  const p = zonedParts(Date.now(), tz)
  const d = new Date(Date.UTC(p.y, p.mo - 1, p.d - offsetDays))
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`
}
