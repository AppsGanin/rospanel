// Branding context: fetches the configured panel name / colour theme / logo and
// applies them app-wide. The theme repaints the whole UI by overriding a handful
// of CSS variables on :root at runtime:
//   accent  → the Tailwind --color-brand-* ramp (every bg-brand-*/text-brand-*)
//   text    → --color-ink         (text-ink + body/headings)
//   muted   → --color-ink-muted   (text-ink-muted)
//   bg      → --ui-bg             (page background, see index.css)
//   surface → --color-white       (bg-white: cards, inputs, modals)
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

type BrandState = BrandingInfo & {
  logoURL: string // custom-logo endpoint (only meaningful when has_custom_logo)
  refresh: () => Promise<void>
  loaded: boolean
}

const FALLBACK: BrandingInfo = {
  panel_name: DEFAULT_NAME,
  theme: DEFAULT_THEME,
  has_custom_logo: false,
  default_name: DEFAULT_NAME,
  default_theme: DEFAULT_THEME,
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
// compiled values show through).
function applyTheme(theme: ThemeColors, def: ThemeColors) {
  const root = document.documentElement
  const set = (name: string, value: string, fallback: string) => {
    if (value && value.toLowerCase() !== fallback.toLowerCase()) {
      root.style.setProperty(name, value)
    } else {
      root.style.removeProperty(name)
    }
  }

  // Accent ramp.
  const base = hexToRgb(theme.accent)
  if (base && theme.accent.toLowerCase() !== def.accent.toLowerCase()) {
    for (const [shade, target, t] of RAMP) {
      root.style.setProperty(`--color-brand-${shade}`, toHex(mix(base, target, t)))
    }
  } else {
    for (const s of SHADES) root.style.removeProperty(`--color-brand-${s}`)
  }

  set('--color-ink', theme.text, def.text)
  set('--color-ink-muted', theme.muted, def.muted)
  set('--ui-bg', theme.bg, def.bg)
  set('--color-white', theme.surface, def.surface)

  // Neutral ramp (borders/dividers/hover/secondary text) derived from the theme.
  const text = hexToRgb(theme.text) ?? hexToRgb(def.text)!
  const surface = hexToRgb(theme.surface) ?? hexToRgb(def.surface)!
  const accent = base ?? hexToRgb(def.accent)!
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
  root.style.setProperty(
    '--accent-fg',
    toHex(darkSurface ? mix(accent, 255, 0.42) : mix(accent, 0, 0.18)),
  )

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

export function BrandProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<BrandingInfo | null>(null)
  const [logoBust, setLogoBust] = useState(0)

  const refresh = useCallback(async () => {
    const b = await getBranding().catch(() => FALLBACK)
    setInfo(b)
    setLogoBust((v) => v + 1)
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  useEffect(() => {
    if (!info) return
    applyTheme(info.theme ?? DEFAULT_THEME, info.default_theme ?? DEFAULT_THEME)
    document.title = info.panel_name || DEFAULT_NAME
  }, [info])

  const value = useMemo<BrandState>(() => {
    const i = info ?? FALLBACK
    return {
      ...i,
      logoURL: `api/branding/logo${logoBust ? `?v=${logoBust}` : ''}`,
      refresh,
      loaded: info !== null,
    }
  }, [info, logoBust, refresh])

  return <BrandCtx.Provider value={value}>{children}</BrandCtx.Provider>
}

export function useBrand(): BrandState {
  const ctx = useContext(BrandCtx)
  if (!ctx) throw new Error('useBrand must be used within BrandProvider')
  return ctx
}
