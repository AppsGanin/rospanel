import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getStatsCountries, type CountryStat } from './api'
import { currentLang } from './i18n'
import { Card, Skeleton } from './ui'

const PALETTE = [
  '#2566f5', '#0d9488', '#9333ea', '#f97316', '#ef4444',
  '#06b6d4', '#65a30d', '#ec4899', '#4f46e5', '#eab308',
]

// flag turns a 2-letter country code into its emoji flag via regional-indicator
// symbols; anything else (the "" unknown bucket) gets a globe.
function flag(code: string): string {
  if (code.length !== 2) return '🌐'
  const base = 0x1f1e6
  const up = code.toUpperCase()
  return String.fromCodePoint(
    base + up.charCodeAt(0) - 65,
    base + up.charCodeAt(1) - 65,
  )
}

function countryName(code: string, lang: string, unknown: string): string {
  if (code.length !== 2) return unknown
  try {
    const dn = new Intl.DisplayNames([lang], { type: 'region' })
    return dn.of(code.toUpperCase()) || code.toUpperCase()
  } catch {
    return code.toUpperCase()
  }
}

// ConnectionCountries shows where recent client connections came from: distinct
// source IPs per country, resolved from geoip.dat. A donut of the busiest countries
// plus a ranked list with a share bar.
export function ConnectionCountries() {
  const { t } = useTranslation()
  const [rows, setRows] = useState<CountryStat[] | null>(null)
  const lang = currentLang()

  useEffect(() => {
    getStatsCountries()
      .then(setRows)
      .catch(() => setRows([]))
  }, [])

  const total = useMemo(
    () => (rows ?? []).reduce((a, r) => a + r.ips, 0),
    [rows],
  )
  const maxIPs = useMemo(
    () => (rows ?? []).reduce((a, r) => Math.max(a, r.ips), 0),
    [rows],
  )

  if (rows === null) {
    return (
      <Card className="p-4">
        <Skeleton className="mb-3 h-5 w-40" />
        <Skeleton className="h-40 w-full rounded-lg" />
      </Card>
    )
  }

  return (
    <Card className="p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="font-bold">{t('stats.byCountry')}</h3>
        <p className="text-sm text-ink-muted">{t('stats.countryTotal', { n: total })}</p>
      </div>
      {rows.length === 0 ? (
        <p className="py-8 text-center text-ink-muted">{t('stats.noCountryData')}</p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {rows.map((r, i) => {
            const pct = maxIPs > 0 ? Math.round((r.ips / maxIPs) * 100) : 0
            return (
              <div key={r.code || 'unknown'} className="flex items-center gap-2 text-sm">
                <span className="w-6 shrink-0 text-center text-base leading-none">
                  {flag(r.code)}
                </span>
                <span className="w-40 shrink-0 truncate">
                  {countryName(r.code, lang, t('stats.unknownCountry'))}
                </span>
                <div className="relative h-4 flex-1 overflow-hidden rounded bg-gray-100">
                  <div
                    className="h-full rounded"
                    style={{
                      width: `${pct}%`,
                      background: PALETTE[i % PALETTE.length],
                      minWidth: r.ips > 0 ? 2 : 0,
                    }}
                  />
                </div>
                <span className="w-24 shrink-0 text-right tabular-nums text-ink-muted">
                  {t('stats.countryIps', { n: r.ips })}
                </span>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}
