import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getStatsASNs,
  getStatsCountries,
  type ASNStat,
  type CountryStat,
} from './api'
import { ShareBar } from './charts'
import { currentLang } from './i18n'
import { EmptyState, Panel, SegmentedControl, Skeletons } from './ui'
import { countryFlag, countryName } from './format'


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
    <Panel
      title={t('stats.byCountry')}
      aside={
        <SegmentedControl
          size="xs"
          value={mode}
          onChange={(v) => setMode(v as 'country' | 'asn')}
          data={[
            { value: 'country', label: t('stats.byCountryTab') },
            { value: 'asn', label: t('stats.byAsnTab') },
          ]}
        />
      }
      pad
    >
      {rows === null ? (
        <div className="flex flex-col gap-2">
          <Skeletons n={5} className="h-4 w-full" />
        </div>
      ) : rows.length === 0 ? (
        <EmptyState title={t('stats.noCountryData')} />
      ) : (
        <div className="flex flex-col gap-2">
          {rows.map((r) => (
            <ShareBar
              key={r.key}
              glyph={r.glyph}
              label={r.label}
              percent={maxIPs > 0 ? (r.ips / maxIPs) * 100 : 0}
              value={t('stats.countryIps', { n: r.ips })}
              title={r.label}
            />
          ))}
          <p className="mt-0.5 text-[11px] text-ink-muted">
            {t('stats.countryTotal', { n: total })}
          </p>
        </div>
      )}
    </Panel>
  )
}
