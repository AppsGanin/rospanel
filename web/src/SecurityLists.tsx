import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getBans, getProbes, getSettings, unbanIP, type Ban, type ProbeHit } from './api'
import { useCan } from './role'
import { countryFlag, countryName } from './format'
import { useAction, useShowMore } from './hooks'
import i18n from './i18n'
import { inPanelTz } from './tz'
import { cn, IconButton, IconUnlock, MICRO, Mono, Panel, ShowMore, useWideBox } from './ui'

// The two lists the security features produce: who has been scanning for the hidden
// panel path, and every address dropped at the firewall. They live on the
// statistics page rather than in the settings that switch them on — a settings card
// is where a rule is written, and neither of these is a setting: they are what the
// rules have caught, read the way the other reports here are read.
//
// Each fetches its own data and renders nothing at all when there is nothing to
// show, so the page stays as short as the install is quiet. Both endpoints are
// admin-level; the caller decides whether the reader is one.

// Both lists are dense grid rows on a shared template — address, what it did, where
// it comes from, when — not boxed cards: they are read down a column like every
// other list in the console. Below WIDE_MIN the row folds onto two lines.
const PROBE_TPL = 'minmax(0,1.1fr) minmax(0,.5fr) minmax(0,1.7fr) minmax(0,1fr)'
const BLOCK_TPL = 'minmax(0,1.1fr) minmax(0,1.6fr) minmax(0,.8fr) minmax(0,1fr) 24px'
const NARROW_TPL = 'minmax(0,1fr) auto'
const WIDE_MIN = 520

const rowCls = 'grid items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]'

function fmtWhen(unix: number): string {
  return new Date(unix * 1000).toLocaleString(
    i18n.language,
    inPanelTz({
      day: '2-digit',
      month: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }),
  )
}

// where reads as one line of prose ("🇩🇪 Германия · OMEGATECH-AS") so it can be a
// single cell wide and a single clause narrow.
function where(country?: string, asn?: number, org?: string): string {
  const parts = []
  if (country)
    parts.push(`${countryFlag(country)} ${countryName(country, i18n.language, country)}`)
  if (org) parts.push(org)
  else if (asn) parts.push(`AS${asn}`)
  return parts.join(' · ')
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
  const [boxRef, wide] = useWideBox(WIDE_MIN)

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
    <Panel
      title={t('general.probeRecent')}
      aside={
        <span className="min-w-0 text-xs text-ink-muted">
          {t('security.probeHint', { days })}
        </span>
      }
    >
      <div ref={boxRef}>
        {wide && (
          <div
            className={cn(MICRO, 'grid items-center gap-3 px-3.5 py-2')}
            style={{ gridTemplateColumns: PROBE_TPL }}
          >
            <span className="truncate">{t('security.colIp')}</span>
            <span className="truncate">{t('security.colPaths')}</span>
            <span className="truncate">{t('security.colWhere')}</span>
            <span className="truncate text-right">{t('security.colWhen')}</span>
          </div>
        )}
        {rows.shown.map((p) => {
          const from = where(p.country, p.asn, p.org)
          return (
            <div
              key={p.ip}
              className={rowCls}
              style={{ gridTemplateColumns: wide ? PROBE_TPL : NARROW_TPL }}
            >
              <Mono className="truncate text-xs text-ink" title={p.ip}>
                {p.ip}
              </Mono>
              {wide ? (
                <>
                  <Mono className="text-[11px] text-ink-muted">{p.paths}</Mono>
                  <span className="truncate text-xs text-ink-muted" title={from}>
                    {from}
                  </span>
                </>
              ) : null}
              <Mono className="text-right text-[11px] text-ink-muted">
                {fmtWhen(p.last_seen)}
              </Mono>
              {!wide && (
                <span className="col-span-2 truncate text-[11px] text-ink-muted">
                  {t('general.probePaths', { n: p.paths })}
                  {from ? ` · ${from}` : ''}
                </span>
              )}
            </div>
          )
        })}
        <ShowMore rest={rows.rest} onClick={rows.showMore} className="p-3.5" />
      </div>
    </Panel>
  )
}

// BlockedList is every address dropped at the firewall, whatever placed it: banned by
// hand from a user's addresses, refused by the source policy, or caught by the proxy's
// brute-force guard or the scanner block — with the button that lets it back in.
// Shown whenever there is something in it.
export function BlockedList() {
  const { t } = useTranslation()
  const canManage = useCan('security.manage')
  const [bans, setBans] = useState<Ban[]>([])
  const [canEnforce, setCanEnforce] = useState(true)
  const { busy, run } = useAction()
  const rows = useShowMore(bans, { first: 10, step: 20, resetKey: bans })
  const [boxRef, wide] = useWideBox(WIDE_MIN)

  const load = () =>
    getBans()
      .then((r) => {
        setBans(r.bans ?? [])
        setCanEnforce(r.can_enforce)
      })
      .catch(() => {})

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load()
  }, [])

  if (bans.length === 0) return null

  // An icon, like the ban button it undoes on a user's addresses; compact, with a
  // negative margin, so it neither makes its row taller nor fills it on hover.
  // Lifting a ban needs security.manage; without it the slot stays empty.
  const unban = (ip: string) => canManage && (
    <IconButton
      compact
      className="-my-1 shrink-0"
      title={t('policy.unblock')}
      disabled={busy}
      onClick={() =>
        run(async () => {
          await unbanIP(ip)
          await load()
        })
      }
    >
      <IconUnlock size={16} />
    </IconButton>
  )

  return (
    <Panel
      title={t('policy.blocked')}
      aside={<span className="min-w-0 text-xs text-ink-muted">{t('security.bansHint')}</span>}
    >
      <div ref={boxRef}>
        {!canEnforce && (
          // Without nftables on the master the bans are recorded and handed to the
          // nodes, but this server drops nothing — said here, not left to be discovered.
          <p className="border-t border-gray-100 px-3.5 py-2 text-xs text-warning">
            {t('policy.noFirewall')}
          </p>
        )}
        {wide && (
          <div
            className={cn(MICRO, 'grid items-center gap-3 px-3.5 py-2')}
            style={{ gridTemplateColumns: BLOCK_TPL }}
          >
            <span className="truncate">{t('security.colIp')}</span>
            <span className="truncate">{t('security.colWhere')}</span>
            <span className="truncate">{t('security.colReason')}</span>
            <span className="truncate text-right">{t('security.colUntil')}</span>
            <span />
          </div>
        )}
        {rows.shown.map((b) => {
          const from = [where(b.country, b.asn, b.org), b.user_name].filter(Boolean).join(' · ')
          const reason = banReason(b.source)
          const until = b.until > 0 ? fmtWhen(b.until) : t('security.untilLifted')
          return (
            <div
              key={`${b.ip} ${b.source}`}
              className={rowCls}
              style={{ gridTemplateColumns: wide ? BLOCK_TPL : NARROW_TPL }}
            >
              <Mono className="truncate text-xs text-ink" title={b.ip}>
                {b.ip}
              </Mono>
              {wide ? (
                <>
                  <span className="truncate text-xs text-ink-muted" title={from}>
                    {from}
                  </span>
                  <span className="truncate text-xs text-warning">{reason}</span>
                </>
              ) : null}
              <Mono className="text-right text-[11px] text-ink-muted">{until}</Mono>
              {wide ? (
                unban(b.ip)
              ) : (
                <span className="col-span-2 flex min-w-0 items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-[11px] text-ink-muted">
                    <span className="text-warning">{reason}</span>
                    {from ? ` · ${from}` : ''}
                  </span>
                  {unban(b.ip)}
                </span>
              )}
            </div>
          )
        })}
        <ShowMore rest={rows.rest} onClick={rows.showMore} className="p-3.5" />
      </div>
    </Panel>
  )
}

// banReason names what placed a ban.
function banReason(source: string): string {
  switch (source) {
    case 'manual':
      return i18n.t('security.banManual')
    case 'asn':
      return i18n.t('policy.reasonASN')
    case 'country':
      return i18n.t('policy.reasonCountry')
    case 'brute':
      return i18n.t('security.banBrute')
    case 'probe':
      return i18n.t('security.banProbe')
  }
  return source
}
