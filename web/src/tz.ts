// IANA timezone helpers shared by the wizard and the settings page.

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
