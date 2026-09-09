import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getConnPolicy, type ConnPolicy } from './api'
import { Panel, Select, SettingRow, TagsInput, TextInput, ToggleRow } from './ui'

// The feature switched off — what the page starts from before the load lands.
export const EMPTY_POLICY: ConnPolicy = {
  mode: 'off',
  countries: [],
  enforce: false,
  block_hours: 24,
}

// ConnPolicyCard is the source policy: which countries a client may connect from,
// and what it has already refused. Enforcement is off until the operator has seen
// what the rule would cut.
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
  // An entry that is not an ISO-2 code is held and shown back instead of vanishing
  // on Enter — a silently dropped entry reads as a broken field, and a silently
  // REWRITTEN one ("germany" → GE, which is Georgia) is worse.
  const [badCountries, setBadCountries] = useState<string[]>([])

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
  }, [])

  const patch = (v: Partial<ConnPolicy>) => onChange({ ...p, ...v })

  const modes = [
    { value: 'off', label: t('policy.modeOff') },
    { value: 'allow', label: t('policy.modeAllow') },
    { value: 'block', label: t('policy.modeBlock') },
  ]
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
    <Panel title={t('policy.title')}>
      <SettingRow
        label={t('policy.mode')}
        hint={t('policy.hint')}
        field={
          <Select
            data={modes}
            value={p.mode}
            onChange={(v) => patch({ mode: v as ConnPolicy['mode'] })}
          />
        }
      />
      {p.mode !== 'off' && (
        <SettingRow label={t('policy.countries')} hint={t('policy.countriesHint')}>
          <>
            <TagsInput value={p.countries} onChange={setCountries} placeholder="RU" />
            {badCountries.length > 0 && (
              <p className="mt-1 text-[11px] text-warning">
                {t('policy.badCountry', { value: badCountries.join(', ') })}
              </p>
            )}
          </>
        </SettingRow>
      )}
      <ToggleRow
        label={t('policy.enforce')}
        hint={canEnforce ? t('policy.enforceHint') : t('policy.noFirewall')}
        checked={p.enforce}
        onChange={(v) => patch({ enforce: v })}
      />
      {p.enforce && (
        <SettingRow
          label={t('policy.blockHours')}
          field={
            <TextInput
              type="number"
              value={String(p.block_hours)}
              onChange={(v) => patch({ block_hours: Number(v.replace(/\D/g, '')) || 0 })}
              placeholder="24"
            />
          }
        />
      )}
      {/* What the rule has already refused — a pointer to the list, not a control. */}
      {blocked > 0 && (
        <SettingRow hint={t('policy.blockedSeeStats', { count: blocked })} />
      )}
    </Panel>
  )
}
