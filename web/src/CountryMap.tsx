import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getStatsASNs,
  getStatsCountries,
  type ASNStat,
  type CountryStat,
} from './api'
import { currentLang } from './i18n'
import { Card, SegmentedControl, Skeleton } from './ui'
import { countryFlag, countryName } from './format'

const PALETTE = [
  '#2566f5', '#0d9488', '#9333ea', '#f97316', '#ef4444',
  '#06b6d4', '#65a30d', '#ec4899', '#4f46e5', '#eab308',
]


// One normalised row for the shared bar renderer: a stable key, a leading glyph, a
// label, and the distinct-IP count.
interface Row {
  key: string
  glyph: string
  label: string
  ips: number
}

// ConnectionCountries shows where recent client connections came from — distinct
// source IPs, broken down either by country (from geoip.dat) or by network operator /
// ASN (from the iptoasn table) — as a ranked list with a share bar.
export function ConnectionCountries() {
  const { t } = useTranslation()
  const lang = currentLang()
  const [mode, setMode] = useState<'country' | 'asn'>('country')
  const [countries, setCountries] = useState<CountryStat[] | null>(null)
  const [asns, setAsns] = useState<ASNStat[] | null>(null)

  useEffect(() => {
    getStatsCountries().then(setCountries).catch(() => setCountries([]))
  }, [])
  useEffect(() => {
    if (mode === 'asn' && asns === null) {
      getStatsASNs().then(setAsns).catch(() => setAsns([]))
    }
  }, [mode, asns])

  const rows: Row[] | null = useMemo(() => {
    if (mode === 'country') {
      return countries === null
        ? null
        : countries.map((r) => ({
            key: r.code || 'unknown',
            glyph: countryFlag(r.code),
            label: countryName(r.code, lang, t('stats.unknownCountry')),
            ips: r.ips,
          }))
    }
    return asns === null
      ? null
      : asns.map((r) => ({
          key: r.asn ? `AS${r.asn}` : 'unknown',
          glyph: '🛰️',
          label: r.org || (r.asn ? `AS${r.asn}` : t('stats.unknownCountry')),
          ips: r.ips,
        }))
  }, [mode, countries, asns, lang, t])

  const total = useMemo(() => (rows ?? []).reduce((a, r) => a + r.ips, 0), [rows])
  const maxIPs = useMemo(
    () => (rows ?? []).reduce((a, r) => Math.max(a, r.ips), 0),
    [rows],
  )

  return (
    <Card className="p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-bold">{t('stats.byCountry')}</h3>
        <div className="flex items-center gap-3">
          {rows !== null && (
            <p className="text-sm text-ink-muted">{t('stats.countryTotal', { n: total })}</p>
          )}
          <SegmentedControl
            value={mode}
            onChange={(v) => setMode(v as 'country' | 'asn')}
            data={[
              { value: 'country', label: t('stats.byCountryTab') },
              { value: 'asn', label: t('stats.byAsnTab') },
            ]}
          />
        </div>
      </div>
      {rows === null ? (
        <Skeleton className="h-40 w-full rounded-lg" />
      ) : rows.length === 0 ? (
        <p className="py-8 text-center text-ink-muted">{t('stats.noCountryData')}</p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {rows.map((r, i) => {
            const pct = maxIPs > 0 ? Math.round((r.ips / maxIPs) * 100) : 0
            return (
              <div key={r.key} className="flex items-center gap-2 text-sm">
                <span className="w-6 shrink-0 text-center text-base leading-none">
                  {r.glyph}
                </span>
                <span className="w-56 shrink-0 truncate" title={r.label}>
                  {r.label}
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
