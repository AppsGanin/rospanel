import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getConnPolicy,
  getProbes,
  getSettings,
  unblockIP,
  type BlockedIP,
  type ProbeHit,
} from './api'
import { countryFlag, countryName } from './format'
import { useAction, useShowMore } from './hooks'
import i18n from './i18n'
import { Badge, Card, ShowMore } from './ui'

// The two lists the security features produce: who has been scanning for the hidden
// panel path, and whose address the source policy refused. They live on the
// statistics page rather than in the settings that switch them on — a settings card
// is where a rule is written, and neither of these is a setting: they are what the
// rules have caught, read the way the other reports here are read.
//
// Each fetches its own data and renders nothing at all when there is nothing to
// show, so the page stays as short as the install is quiet. Both endpoints are
// admin-level; the caller decides whether the reader is one.

// row is the shared shape of both lists: an address, where it belongs, and a
// right-hand column.
function row(children: React.ReactNode, key: string) {
  return (
    <div
      key={key}
      className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg border border-gray-200/70 bg-gray-50/60 px-3 py-1.5 text-sm"
    >
      {children}
    </div>
  )
}

function Where({ country, asn, org }: { country?: string; asn?: number; org?: string }) {
  return (
    <>
      {country && (
        <span className="text-xs text-ink-muted">
          {countryFlag(country)} {countryName(country, i18n.language, country)}
        </span>
      )}
      {org && (
        <span
          className="max-w-[16rem] truncate text-xs text-ink-muted"
          title={asn ? `AS${asn} · ${org}` : org}
        >
          {org}
        </span>
      )}
    </>
  )
}

// ProbeList is the addresses caught scanning for the hidden panel path. Rendered
// only while the detection is on: with it off the rows are a leftover, and a report
// nobody is feeding reads as a live one.
export function ProbeList() {
  const { t } = useTranslation()
  const [on, setOn] = useState(false)
  const [probes, setProbes] = useState<ProbeHit[]>([])
  const [days, setDays] = useState(0)
  const rows = useShowMore(probes, { first: 10, step: 20, resetKey: probes })

  useEffect(() => {
    getSettings()
      .then((s) => {
        setOn(s.probe_detect)
        if (s.probe_detect)
          return getProbes().then((r) => {
            setProbes(r.probes ?? [])
            setDays(r.retention_days)
          })
      })
      .catch(() => {})
  }, [])

  if (!on || probes.length === 0) return null
  return (
    <Card className="p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="font-bold">{t('general.probeRecent')}</h3>
        <span className="text-xs text-ink-muted">{t('security.probeHint', { days })}</span>
      </div>
      <div className="flex flex-col gap-1">
        {rows.shown.map((p) =>
          row(
            <>
              <code className="font-mono text-ink">{p.ip}</code>
              <span className="text-xs text-ink-muted">{t('general.probePaths', { n: p.paths })}</span>
              <Where country={p.country} asn={p.asn} org={p.org} />
              <span className="ml-auto text-xs text-ink-muted">
                {new Date(p.last_seen * 1000).toLocaleString(i18n.language)}
              </span>
            </>,
            p.ip,
          ),
        )}
        <ShowMore rest={rows.rest} onClick={rows.showMore} className="mt-1" />
      </div>
    </Card>
  )
}

// BlockedList is what the source policy refused, with the button that overrules it.
// Shown whenever there is something in it — including after the rule was switched
// off, so nothing stays cut without a way to see it.
export function BlockedList() {
  const { t } = useTranslation()
  const [blocked, setBlocked] = useState<BlockedIP[]>([])
  const { busy, run } = useAction()
  const rows = useShowMore(blocked, { first: 10, step: 20, resetKey: blocked })

  const load = () =>
    getConnPolicy()
      .then((info) => setBlocked(info.blocked ?? []))
      .catch(() => {})

  useEffect(() => {
    load()
  }, [])

  if (blocked.length === 0) return null
  return (
    <Card className="p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="font-bold">{t('policy.blocked')}</h3>
        <span className="text-xs text-ink-muted">{t('security.blockedHint')}</span>
      </div>
      <div className="flex flex-col gap-1">
        {rows.shown.map((b) =>
          row(
            <>
              <code className="font-mono text-ink">{b.ip}</code>
              <Where country={b.country} asn={b.asn} org={b.org} />
              <Badge color="orange" size="xs">
                {t(b.reason === 'asn' ? 'policy.reasonASN' : 'policy.reasonCountry')}
              </Badge>
              <span className="ml-auto text-xs text-ink-muted">
                {t('policy.until', { when: new Date(b.until * 1000).toLocaleString(i18n.language) })}
              </span>
              <button
                type="button"
                className="text-xs text-brand hover:underline disabled:opacity-50"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await unblockIP(b.ip)
                    await load()
                  })
                }
              >
                {t('policy.unblock')}
              </button>
            </>,
            b.ip,
          ),
        )}
        <ShowMore rest={rows.rest} onClick={rows.showMore} className="mt-1" />
      </div>
    </Card>
  )
}
