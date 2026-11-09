// Branding context: fetches the configured panel name / colour theme / logo and
// applies them app-wide. The theme repaints the whole UI by overriding a handful
// of CSS variables on :root at runtime:
//   accent  → the Tailwind --color-brand-* ramp (every bg-brand-*/text-brand-*)
//   text    → --color-ink         (text-ink + body/headings)
//   muted   → --color-ink-muted   (text-ink-muted)
//   bg      → --ui-bg             (page background, see index.css)
//   surface → --color-white       (bg-white: cards, inputs, modals)
// There are two themes, light and dark: the panel shows the one the system asks
// for, unless the admin picked one in the top bar (kept in this browser).
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { getBranding, type BrandingInfo, type ThemeColors } from './api'

const DEFAULT_NAME = 'РосПанель'
const DEFAULT_THEME: ThemeColors = {
  accent: '#0d4cd3',
  text: '#0a1b2e',
  muted: '#5b6b7e',
  bg: '#eaf1fb',
  surface: '#ffffff',
}
// Must match internal/branding.DefaultDarkTheme; its accent is the light one's.
const DEFAULT_DARK: ThemeColors = {
  accent: '',
  text: '#e4ebf5',
  muted: '#8e9db2',
  bg: '#0b1220',
  surface: '#151e30',
}

// The admin's own pick in the top bar: follow the system, or hold one theme.
export type ThemeMode = 'auto' | 'light' | 'dark'

type BrandState = BrandingInfo & {
  logoURL: string // custom-logo endpoint (only meaningful when has_custom_logo)
  refresh: () => Promise<void>
  loaded: boolean
  mode: ThemeMode
  setMode: (m: ThemeMode) => void
  dark: boolean // the dark theme is on screen now
}

const FALLBACK: BrandingInfo = {
  panel_name: DEFAULT_NAME,
  theme: DEFAULT_THEME,
  theme_dark: { ...DEFAULT_DARK, accent: DEFAULT_THEME.accent },
  has_custom_logo: false,
  default_name: DEFAULT_NAME,
  default_theme: DEFAULT_THEME,
  default_theme_dark: DEFAULT_DARK,
}

const MODE_KEY = 'rp-theme-mode'
const CACHE_KEY = 'rp-brand-palette'

function readMode(): ThemeMode {
  try {
    const v = localStorage.getItem(MODE_KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    /* storage blocked: follow the system */
  }
  return 'auto'
}

const darkQuery = () =>
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null

// wantsDark: the dark theme shows when the admin picked it or the system asks for it.
function wantsDark(mode: ThemeMode, sysDark: boolean): boolean {
  if (mode !== 'auto') return mode === 'dark'
  return sysDark
}

// paint applies whichever theme should show now. The stock light theme is what
// the compiled CSS already has, so its defaults clear the overrides; the dark one
// has no compiled twin and sets every variable.
function paint(info: BrandingInfo, mode: ThemeMode, sysDark: boolean) {
  if (wantsDark(mode, sysDark)) applyTheme(info.theme_dark, null)
  else applyTheme(info.theme ?? DEFAULT_THEME, info.default_theme ?? DEFAULT_THEME)
}

const BrandCtx = createContext<BrandState | null>(null)

// hexToRgb parses #rrggbb → [r,g,b]; returns null on malformed input.
function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#([0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  const n = parseInt(m[1], 16)
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff]
}

function toHex([r, g, b]: [number, number, number]): string {
  const c = (v: number) => Math.round(Math.max(0, Math.min(255, v))).toString(16).padStart(2, '0')
  return `#${c(r)}${c(g)}${c(b)}`
}

function mix(rgb: [number, number, number], target: number, t: number): [number, number, number] {
  return [
    rgb[0] + (target - rgb[0]) * t,
    rgb[1] + (target - rgb[1]) * t,
    rgb[2] + (target - rgb[2]) * t,
  ]
}

// mix2 linearly blends two colours: t=0 → a, t=1 → b.
function mix2(
  a: [number, number, number],
  b: [number, number, number],
  t: number,
): [number, number, number] {
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t]
}

// The neutral "gray" ramp is derived from the theme so borders, dividers, hover
// fills and secondary text all follow it: each shade interpolates surface→text by
// lightness, then takes a small nudge toward the accent for a cohesive tint.
// [shade, lightness 0(light)..1(dark), accent tint 0..1]
const GRAY: Array<[number, number, number]> = [
  [50, 0.03, 0.06],
  [100, 0.06, 0.08],
  [200, 0.11, 0.1],
  [300, 0.18, 0.1],
  [400, 0.38, 0.08],
  [500, 0.52, 0.07],
  [600, 0.64, 0.06],
  [700, 0.74, 0.05],
  [800, 0.84, 0.03],
  [900, 0.92, 0.02],
  [950, 0.97, 0.0],
]

// Tints toward white (50–500) and shades toward black (700–900), with the chosen
// accent anchored at 600 — the shade used for primary buttons/links.
const RAMP: Array<[shade: number, target: number, t: number]> = [
  [50, 255, 0.92],
  [100, 255, 0.82],
  [200, 255, 0.62],
  [300, 255, 0.42],
  [400, 255, 0.22],
  [500, 255, 0.1],
  [600, 0, 0],
  [700, 0, 0.16],
  [800, 0, 0.32],
  [900, 0, 0.46],
]

const SHADES = RAMP.map(([s]) => s)

// applyTheme overrides the brand ramp + text/bg/surface variables on :root, or
// clears each override when it matches the stock default (so the hand-tuned
// compiled values show through). A null default sets every variable.
function applyTheme(theme: ThemeColors, def: ThemeColors | null) {
  const root = document.documentElement
  const set = (name: string, value: string, fallback: string | undefined) => {
    if (value && (fallback === undefined || value.toLowerCase() !== fallback.toLowerCase())) {
      root.style.setProperty(name, value)
    } else {
      root.style.removeProperty(name)
    }
  }

  // Accent ramp.
  const base = hexToRgb(theme.accent)
  if (base && (!def || theme.accent.toLowerCase() !== def.accent.toLowerCase())) {
    for (const [shade, target, t] of RAMP) {
      root.style.setProperty(`--color-brand-${shade}`, toHex(mix(base, target, t)))
    }
  } else {
    for (const s of SHADES) root.style.removeProperty(`--color-brand-${s}`)
  }

  set('--color-ink', theme.text, def?.text)
  set('--color-ink-muted', theme.muted, def?.muted)
  set('--ui-bg', theme.bg, def?.bg)
  set('--color-white', theme.surface, def?.surface)

  // Neutral ramp (borders/dividers/hover/secondary text) derived from the theme.
  const fb = def ?? DEFAULT_THEME
  const text = hexToRgb(theme.text) ?? hexToRgb(fb.text)!
  const surface = hexToRgb(theme.surface) ?? hexToRgb(fb.surface)!
  const accent = base ?? hexToRgb(fb.accent)!
  for (const [shade, light, tint] of GRAY) {
    root.style.setProperty(
      `--color-gray-${shade}`,
      toHex(mix2(mix2(surface, text, light), accent, tint)),
    )
  }

  // --accent-fg: the accent used for TEXT/icons that sit on a surface (links,
  // labels, light chips). Lightened on dark surfaces / darkened on light ones so
  // it stays readable for any accent+surface combo (a plain brand-700 would be
  // dark accent on a dark surface). Always set — `.text-accent` references it.
  const darkSurface = luminance(surface) < 0.4
  // The browser's own controls (scrollbars, date pickers, autofill) follow suit.
  root.style.colorScheme = darkSurface ? 'dark' : 'light'
  root.style.setProperty(
    '--accent-fg',
    toHex(darkSurface ? mix(accent, 255, 0.42) : mix(accent, 0, 0.18)),
  )

  // --color-onbrand: text/icons on the accent fill — the primary button, a
  // selected segment, a ticked checkbox. Must match internal/branding.OnFill.
  root.style.setProperty('--color-onbrand', onFill(accent))

  // Status text colours: fixed hue (green/orange/red), lightened on dark surfaces
  // so the meaning is preserved while staying readable in any theme.
  const STATUS: Array<[string, [number, number, number]]> = [
    ['--success-fg', [5, 150, 105]], // emerald #059669
    ['--warning-fg', [234, 88, 12]], // orange  #ea580c
    ['--danger-fg', [220, 38, 38]], // red     #dc2626
  ]
  for (const [name, rgb] of STATUS) {
    root.style.setProperty(name, toHex(darkSurface ? mix(rgb, 255, 0.4) : mix(rgb, 0, 0.12)))
  }
}

// Relative luminance (sRGB) 0..1 — used to decide if a surface reads as dark.
function luminance([r, g, b]: [number, number, number]): number {
  const f = (c: number) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
}

// ON_FILL_DARK is the ink put on an accent too light for white text.
const ON_FILL_DARK = '#0a1b2e'

// onFill picks the text colour for a fill of the given colour. White stays until
// it falls below 3:1 against the fill (WCAG's floor for bold UI text), which is
// luminance 0.3: every stock swatch keeps its white label, and only a genuinely
// light accent — white, yellow, a pale orange — gets dark ink instead.
function onFill(fill: [number, number, number]): string {
  return luminance(fill) > 0.3 ? ON_FILL_DARK : '#ffffff'
}

// The last palette this browser saw, painted before the first fetch returns, so
// a dark panel does not flash light on every load.
function cachedInfo(): BrandingInfo | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    return raw ? (JSON.parse(raw) as BrandingInfo) : null
  } catch {
    return null
  }
}
paint(cachedInfo() ?? FALLBACK, readMode(), darkQuery()?.matches ?? false)

export function BrandProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<BrandingInfo | null>(null)
  const [logoBust, setLogoBust] = useState(0)
  const [mode, setModeState] = useState<ThemeMode>(readMode)
  const [sysDark, setSysDark] = useState(() => darkQuery()?.matches ?? false)

  const refresh = useCallback(async () => {
    const b = await getBranding().catch(() => FALLBACK)
    setInfo(b)
    setLogoBust((v) => v + 1)
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  // Follow the system flipping while the panel is open.
  useEffect(() => {
    const q = darkQuery()
    if (!q) return
    const on = () => setSysDark(q.matches)
    q.addEventListener('change', on)
    return () => q.removeEventListener('change', on)
  }, [])

  const setMode = useCallback((m: ThemeMode) => {
    setModeState(m)
    try {
      if (m === 'auto') localStorage.removeItem(MODE_KEY)
      else localStorage.setItem(MODE_KEY, m)
    } catch {
      /* storage blocked: the pick lasts this tab */
    }
  }, [])

  useEffect(() => {
    if (!info) return
    paint(info, mode, sysDark)
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(info))
    } catch {
      /* no cache: the first paint is the stock theme */
    }
    document.title = info.panel_name || DEFAULT_NAME
  }, [info, mode, sysDark])

  const value = useMemo<BrandState>(() => {
    const i = info ?? FALLBACK
    return {
      ...i,
      logoURL: `api/branding/logo${logoBust ? `?v=${logoBust}` : ''}`,
      refresh,
      loaded: info !== null,
      mode,
      setMode,
      dark: wantsDark(mode, sysDark),
    }
  }, [info, logoBust, refresh, mode, setMode, sysDark])

  return <BrandCtx.Provider value={value}>{children}</BrandCtx.Provider>
}

export function useBrand(): BrandState {
  const ctx = useContext(BrandCtx)
  if (!ctx) throw new Error('useBrand must be used within BrandProvider')
  return ctx
}
