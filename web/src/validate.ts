// Client-side validators for the ACME form. These mirror the server checks in
// internal/core/validate.go — they exist only for instant feedback; the server
// is still the authority.

// isValidEmail reports whether s is a single, well-formed e-mail with a dotted
// domain part (e.g. "a@localhost" is rejected, as ACME CAs won't accept it).
export function isValidEmail(s: string): boolean {
  const v = s.trim()
  // local@domain, no spaces, exactly one @, domain has a dot and TLD ≥ 2 chars.
  return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(v)
}

// isValidDomain reports whether s is a syntactically valid DNS hostname (FQDN
// with at least one dot, labels 1–63 chars of [A-Za-z0-9-], no leading/trailing
// hyphen). Not an IP — use isIP for that.
export function isValidDomain(s: string): boolean {
  const v = s.trim().replace(/\.$/, '')
  if (v.length === 0 || v.length > 253 || !v.includes('.')) return false
  const labels = v.split('.')
  const labelOk = labels.every((label) =>
    /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(label),
  )
  // The TLD (last label) is never all-numeric — also rejects IPv4 addresses,
  // which are syntactically valid label sequences.
  return labelOk && /[A-Za-z-]/.test(labels[labels.length - 1])
}

// isIP reports whether s is a valid IPv4 or IPv6 address.
export function isIP(s: string): boolean {
  const v = s.trim()
  // IPv4: four 0–255 octets.
  if (/^(\d{1,3}\.){3}\d{1,3}$/.test(v)) {
    return v.split('.').every((o) => Number(o) <= 255)
  }
  // IPv6: loose but practical — hex groups and colons, optional "::".
  return /^[0-9a-fA-F:]+$/.test(v) && v.includes(':')
}

// isValidACMETarget reports whether target is acceptable: Let's Encrypt accepts
// a domain OR an IP; ZeroSSL accepts domains only.
export function isValidACMETarget(target: string, zerossl: boolean): boolean {
  if (isValidDomain(target)) return true
  return !zerossl && isIP(target)
}

// RESERVED_SUB_PATHS mirrors reservedSubPaths in internal/core/manager_settings.go:
// first-segment names the subscription prefix must not use (panel/system routes).
const RESERVED_SUB_PATHS = new Set(['api', 'assets', 'login', 'logout', 'favicon', 'static', 'well-known'])

// subPathError validates the subscription path against the same rules as the
// server and returns an operator-facing error message, or null when valid.
// secret is the panel's secret path (the subscription must not shadow it).
export function subPathError(path: string, secret: string): string | null {
  const p = path.trim()
  if (!/^[A-Za-z0-9_-]{1,32}$/.test(p)) {
    return 'Путь подписки: латиница, цифры, «-» и «_», 1–32 символа.'
  }
  if (RESERVED_SUB_PATHS.has(p.toLowerCase())) {
    return `Путь «${p}» зарезервирован панелью — выберите другой.`
  }
  if (secret && p.toLowerCase() === secret.toLowerCase()) {
    return 'Путь подписки не может совпадать с секретным путём панели.'
  }
  return null
}
