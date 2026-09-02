import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getConnPolicy, type ConnPolicy } from './api'
import { useAction } from './hooks'
import { notifySuccess } from './notify'
import { Button, Select, SettingCard, TagsInput, TextInput, ToggleRow } from './ui'

// The feature switched off — what the page starts from before the load lands.
export const EMPTY_POLICY: ConnPolicy = {
  mode: 'off',
  countries: [],
  asns: [],
  enforce: false,
  block_hours: 24,
}

// ConnPolicyCard is the source policy: where a client is allowed to connect from,
// and who it has refused. The two lists are separate on purpose — the country rule
// is the operator's market, the ASN list is the hosting networks a resold account
// tends to appear from — and enforcement is off until they have seen what the rule
// would cut.
export function ConnPolicyCard({
  value: p,
  onChange,
}: {
  value: ConnPolicy
  onChange: (v: ConnPolicy) => void
}) {
  const { t } = useTranslation()
  // What the rule has caught is read here, not edited: the count points at the
  // statistics page, and whether this machine can enforce at all comes with it.
  const [blocked, setBlocked] = useState(0)
  const [canEnforce, setCanEnforce] = useState(true)
  // Entries that are neither an ISO-2 code nor an AS number are held and shown back
  // instead of vanishing on Enter — a silently dropped entry reads as a broken
  // field, and a silently REWRITTEN one ("germany" → GE, which is Georgia) is worse.
  const [badCountries, setBadCountries] = useState<string[]>([])
  const [badASNs, setBadASNs] = useState<string[]>([])

  useEffect(() => {
    getConnPolicy()
      .then((info) => {
        setBlocked((info.blocked ?? []).length)
        setCanEnforce(info.can_enforce)
      })
      .catch(() => {})
    // Read once with the page: this is a pointer to the statistics list, not a live
    // counter, and re-reading it on every keystroke of the draft would be a request
    // per character.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const patch = (v: Partial<ConnPolicy>) => onChange({ ...p, ...v })

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

        {blocked > 0 && (
          <p className="text-xs text-ink-muted">{t('policy.blockedSeeStats', { count: blocked })}</p>
        )}
      </div>
    </SettingCard>
  )
}
