import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getConnPolicy,
  saveConnPolicy,
  unblockIP,
  type BlockedIP,
  type ConnPolicy,
} from './api'
import { countryFlag, countryName } from './format'
import { useAction, useShowMore } from './hooks'
import i18n from './i18n'
import { notifySuccess } from './notify'
import { Badge, Button, Select, SettingCard, ShowMore, TagsInput, TextInput, ToggleRow } from './ui'

const EMPTY: ConnPolicy = { mode: 'off', countries: [], asns: [], enforce: false, block_hours: 24 }

// ConnPolicyCard is the source policy: where a client is allowed to connect from,
// and who it has refused. The two lists are separate on purpose — the country rule
// is the operator's market, the ASN list is the hosting networks a resold account
// tends to appear from — and enforcement is off until they have seen what the rule
// would cut.
export function ConnPolicyCard() {
  const { t } = useTranslation()
  const [p, setP] = useState<ConnPolicy>(EMPTY)
  const [saved, setSaved] = useState<ConnPolicy>(EMPTY)
  const [blocked, setBlocked] = useState<BlockedIP[]>([])
  const [canEnforce, setCanEnforce] = useState(true)
  const { busy, run } = useAction()
  const rows = useShowMore(blocked, { first: 5, step: 20, resetKey: blocked })
  // The two lists are edited as text and stored as data (ISO-2 codes, AS numbers),
  // so what the operator typed and what the panel keeps can disagree. Entries that
  // are not one of those are held here and shown back instead of vanishing on Enter
  // — a silently dropped entry reads as a broken field, and a silently REWRITTEN one
  // ("germany" → GE, which is Georgia) is worse than either.
  const [badCountries, setBadCountries] = useState<string[]>([])
  const [badASNs, setBadASNs] = useState<string[]>([])

  const load = () =>
    getConnPolicy()
      .then((info) => {
        setP(info.policy)
        setSaved(info.policy)
        setBadCountries([])
        setBadASNs([])
        setBlocked(info.blocked ?? [])
        setCanEnforce(info.can_enforce)
      })
      .catch(() => {})

  useEffect(() => {
    load()
  }, [])

  const patch = (v: Partial<ConnPolicy>) => setP((cur) => ({ ...cur, ...v }))
  const dirty = JSON.stringify(p) !== JSON.stringify(saved)
  const save = () =>
    run(async () => {
      await saveConnPolicy(p)
      notifySuccess(t('common.saved'))
      await load()
    })
  const lift = (ip: string) =>
    run(async () => {
      await unblockIP(ip)
      await load()
    })

  const modes = [
    { value: 'off', label: t('policy.modeOff') },
    { value: 'allow', label: t('policy.modeAllow') },
    { value: 'block', label: t('policy.modeBlock') },
  ]
  // ASNs are numbers to the server; the chips stay the plain number the operator
  // typed (an "AS" prefix is accepted and stripped, so pasting AS16509 works).
  const asnChips = p.asns.map(String)
  const setASNs = (chips: string[]) => {
    const good: number[] = []
    const bad: string[] = []
    for (const raw of chips) {
      const n = Number(raw.replace(/^as/i, '').trim())
      if (Number.isInteger(n) && n > 0) good.push(n)
      else bad.push(raw)
    }
    setBadASNs(bad)
    patch({ asns: [...new Set(good)] })
  }
  const setCountries = (chips: string[]) => {
    const good: string[] = []
    const bad: string[] = []
    for (const raw of chips) {
      const cc = raw.trim().toUpperCase()
      if (/^[A-Z]{2}$/.test(cc)) good.push(cc)
      else bad.push(raw)
    }
    setBadCountries(bad)
    patch({ countries: [...new Set(good)] })
  }

  return (
    <SettingCard title={t('policy.title')} description={t('policy.hint')}>
      <div className="flex flex-col gap-3">
        <Select
          label={t('policy.mode')}
          data={modes}
          value={p.mode}
          onChange={(v) => patch({ mode: v as ConnPolicy['mode'] })}
        />
        {p.mode !== 'off' && (
          <div>
            <TagsInput
              label={t('policy.countries')}
              hint={t('policy.countriesHint')}
              value={p.countries}
              onChange={setCountries}
              placeholder="RU"
            />
            {badCountries.length > 0 && (
              <p className="mt-1 text-xs text-warning">
                {t('policy.badCountry', { value: badCountries.join(', ') })}
              </p>
            )}
          </div>
        )}
        <div>
          <TagsInput
            label={t('policy.asns')}
            hint={t('policy.asnsHint')}
            value={asnChips}
            onChange={setASNs}
            placeholder="16509"
          />
          {badASNs.length > 0 && (
            <p className="mt-1 text-xs text-warning">
              {t('policy.badASN', { value: badASNs.join(', ') })}
            </p>
          )}
        </div>
        <ToggleRow
          label={t('policy.enforce')}
          hint={canEnforce ? t('policy.enforceHint') : t('policy.noFirewall')}
          checked={p.enforce}
          onChange={(v) => patch({ enforce: v })}
        />
        {p.enforce && (
          <TextInput
            label={t('policy.blockHours')}
            type="number"
            value={String(p.block_hours)}
            onChange={(v) => patch({ block_hours: Number(v.replace(/\D/g, '')) || 0 })}
            placeholder="24"
          />
        )}
        {dirty && (
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="light" color="gray" disabled={busy} onClick={() => setP(saved)}>
              {t('common.cancel')}
            </Button>
            <Button size="sm" loading={busy} onClick={save}>
              {t('common.save')}
            </Button>
          </div>
        )}

        {blocked.length > 0 && (
          <div className="flex flex-col gap-1 border-t border-gray-100 pt-3">
            <p className="text-xs font-medium uppercase tracking-wide text-ink-muted">
              {t('policy.blocked')}
            </p>
            {rows.shown.map((b) => (
              <div
                key={b.ip}
                className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg border border-gray-200/70 bg-gray-50/60 px-3 py-1.5 text-sm"
              >
                <code className="font-mono text-ink">{b.ip}</code>
                {b.country && (
                  <span className="text-xs text-ink-muted">
                    {countryFlag(b.country)} {countryName(b.country, i18n.language, b.country)}
                  </span>
                )}
                {b.asn > 0 && (
                  <span className="max-w-[16rem] truncate text-xs text-ink-muted" title={b.org}>
                    AS{b.asn} {b.org}
                  </span>
                )}
                <Badge color="orange" size="xs">
                  {t(b.reason === 'asn' ? 'policy.reasonASN' : 'policy.reasonCountry')}
                </Badge>
                <span className="ml-auto text-xs text-ink-muted">
                  {t('policy.until', { when: new Date(b.until * 1000).toLocaleString(i18n.language) })}
                </span>
                <button
                  type="button"
                  className="text-xs text-brand hover:underline"
                  disabled={busy}
                  onClick={() => lift(b.ip)}
                >
                  {t('policy.unblock')}
                </button>
              </div>
            ))}
            <ShowMore rest={rows.rest} onClick={rows.showMore} className="mt-1" />
          </div>
        )}
      </div>
    </SettingCard>
  )
}
