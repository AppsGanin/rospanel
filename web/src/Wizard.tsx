import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { finishSetup, getTLS, regenSecret, setACME, setupPassword, setupTimezone } from './api'
import type { TLSStatus } from './api'
import { AuthShell } from './AuthShell'
import { errMessage, notifyError } from './notify'
import { BACKUP_ACCEPT, ManifestCard, RestoreWaiting, useRestore, ValidationNote } from './restore'
import { browserTimezone, tzOptions } from './tz'
import { Button, cn, Code, IconCheck, PasswordInput, Select, TextInput } from './ui'
import { isIP, isValidACMETarget, isValidEmail } from './validate'

// The five steps the indicator names, "Начало" (the new-or-restore choice) among
// them: an indicator that starts at the second step tells the reader nothing about
// where they came from.
const STEP_KEYS = [
  'wizard.stepStart',
  'wizard.stepPassword',
  'wizard.stepTime',
  'wizard.stepAddress',
  'wizard.stepPath',
] as const

// ChoiceRow is how the wizard asks a question: a full-width row, not a bare radio.
// The whole row is the target, the tick is filled with the accent when it is the
// answer, and the explanation sits under the title where it can be read before
// choosing rather than after.
function ChoiceRow({
  checked,
  onSelect,
  title,
  hint,
  children,
}: {
  checked: boolean
  onSelect: () => void
  title: ReactNode
  hint?: ReactNode
  children?: ReactNode
}) {
  return (
    <label
      className={cn(
        'relative flex cursor-pointer items-start gap-3 rounded-xl border p-3.5 transition',
        checked
          ? 'accent-tint border-accent'
          : 'border-gray-200 bg-white hover:border-gray-300 hover:bg-gray-50',
      )}
    >
      <input
        type="radio"
        className="sr-only"
        checked={checked}
        onChange={onSelect}
      />
      <span
        className={cn(
          'mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border transition',
          checked ? 'border-brand-600 bg-brand-600 text-onaccent' : 'border-gray-300 bg-white',
        )}
      >
        {checked && <IconCheck size={12} />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-semibold text-ink">{title}</span>
        {hint && <span className="mt-0.5 block text-xs leading-relaxed text-ink-muted">{hint}</span>}
        {children}
      </span>
    </label>
  )
}

function currentSecret(): string {
  return window.location.pathname.split('/').filter(Boolean)[0] || 'rospanel'
}

// ── Restore flow ─────────────────────────────────────────────────────────────

function RestoreFlow({ onBack }: { onBack: () => void }) {
  const { t } = useTranslation()
  const { fileRef, file, inspection, manifest, inspecting, restoring, done, pick, restore } = useRestore()

  if (done) return <RestoreWaiting manifest={done} currentDomain={window.location.hostname} />

  return (
    <div className="flex flex-col gap-4">
      <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.restoreIntro')}</p>
      <input
        ref={fileRef}
        type="file"
        accept={BACKUP_ACCEPT}
        className="hidden"
        onChange={(e) => pick(e.target.files?.[0] ?? null)}
      />
      <Button variant="light" color="gray" loading={inspecting} onClick={() => fileRef.current?.click()}>
        {file ? file.name : t('wizard.pickFile')}
      </Button>

      {manifest && <ManifestCard m={manifest} label={t('wizard.inBackup')} />}
      {inspection && <ValidationNote inspection={inspection} />}

      <div className="flex items-center justify-between">
        <Button variant="outline" color="gray" onClick={onBack}>
          {t('common.back')}
        </Button>
        {manifest && (
          <Button
            color="red"
            loading={restoring}
            disabled={!inspection?.valid}
            // First run: there is no admin password to step up against yet, and the
            // panel's verifyStepUp waives the check until setup is done.
            onClick={() => restore('')}
          >
            {t('wizard.restoreAndRestart')}
          </Button>
        )}
      </div>
    </div>
  )
}

// ── Main Wizard ───────────────────────────────────────────────────────────────

export function Wizard({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'' | 'new' | 'restore'>('')
  const [active, setActive] = useState(0)
  const defaultTz = useMemo(browserTimezone, [])
  const tzData = useMemo(() => tzOptions(defaultTz), [defaultTz])

  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [timezone, setTimezone] = useState(defaultTz)
  const [wizMode, setWizMode] = useState<'ip' | 'domain' | 'keep'>('ip')
  const [domain, setDomain] = useState('')
  const [email, setEmail] = useState('')
  const [provider, setProvider] = useState('letsencrypt')
  const [finalHost, setFinalHost] = useState('')
  const [pathMode, setPathMode] = useState<'generate' | 'keep'>('generate')
  const [savedPath, setSavedPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [tls, setTls] = useState<TLSStatus | null>(null)

  // Reflect the panel's actual current address. If it was installed with a
  // domain (ROSPANEL_HOST set), it already serves a domain certificate — the
  // address step must say so and default to keeping it, not claim "works over
  // IP". On failure we silently fall back to the IP wording.
  useEffect(() => {
    getTLS()
      .then((t) => {
        setTls(t)
        const h = (t.domain || '').trim()
        const valid = !!t.cert && !!t.cert.issuer && t.cert.issuer !== t.cert.subject
        if (h && !isIP(h) && valid) {
          // Real domain certificate already in place — default to keeping it.
          setWizMode('keep')
        } else if (h && !isIP(h)) {
          // Domain configured but only a temporary self-signed cert (ACME has
          // not issued yet) — default to (re)issuing it, with the domain
          // prefilled so the user just confirms.
          setWizMode('domain')
          setDomain(h)
          setProvider(t.acme_provider || 'letsencrypt')
          if (t.acme_email) setEmail(t.acme_email)
        }
      })
      .catch(() => {})
  }, [])

  const cert = tls?.cert
  // A real CA cert has issuer ≠ subject; a self-signed fallback has them equal
  // (mirrors the settings TLS panel's "valid vs temporary" distinction).
  const certValid = !!cert && !!cert.issuer && cert.issuer !== cert.subject
  const curHost = (tls?.domain || '').trim()
  const curIsDomain = curHost !== '' && !isIP(curHost)
  const onDomainWithCert = curIsDomain && certValid

  // Live ACME validation, mirroring the settings TLS panel (TLSPanel.tsx):
  // Let's Encrypt accepts a domain or an IP, ZeroSSL domains only; e-mail is
  // required for ZeroSSL. These drive the inline errors + the disabled button.
  const isZeroSSL = provider === 'zerossl'
  const domainTrim = domain.trim()
  const emailTrim = email.trim()
  const targetErr = domainTrim !== '' && !isValidACMETarget(domainTrim, isZeroSSL)
  const emailErr = emailTrim !== '' && !isValidEmail(emailTrim)
  const emailMissing = isZeroSSL && emailTrim === ''
  const domainInvalid = domainTrim === '' || targetErr || emailErr || emailMissing

  const savePassword = async () => {
    if (password.length < 8) return notifyError(t('password.tooShort'))
    if (password !== confirm) return notifyError(t('password.mismatch'))
    setBusy(true)
    try {
      await setupPassword(password)
      setActive(1)
    } catch (e) {
      notifyError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const saveTimezone = async () => {
    setBusy(true)
    try {
      await setupTimezone(timezone || '')
      setActive(2)
    } catch (e) {
      notifyError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const advanceAddress = async () => {
    if (wizMode === 'domain') {
      // The button is disabled until the inputs are valid (see domainInvalid),
      // so here we just request the cert and surface any server-side failure.
      setBusy(true)
      try {
        await setACME(domainTrim, emailTrim, provider)
        setFinalHost(domainTrim)
        setActive(3)
      } catch (e) {
        notifyError(errMessage(e, t('wizard.certFailed')))
      } finally {
        setBusy(false)
      }
    } else if (wizMode === 'keep') {
      // 'keep' = don't issue anything. Case A (real domain cert) lands on the
      // domain; Case B (kept a temporary self-signed cert, where the domain may
      // be unreachable) stays on the current host so we never strand the user.
      setFinalHost(onDomainWithCert ? curHost : window.location.hostname)
      setActive(3)
    } else {
      setFinalHost(window.location.hostname)
      setActive(3)
    }
  }

  const redirect = (path: string) => {
    const host = finalHost || window.location.hostname
    const go = () => {
      window.location.href = `https://${host}/${path}/`
    }
    if (wizMode === 'domain') setTimeout(go, 2500)
    else go()
  }

  const finishGenerate = async () => {
    setBusy(true)
    try {
      await finishSetup()
      const { secret_path } = await regenSecret()
      // Don't redirect straight away — the new path is the only way back into
      // the panel and can't be recovered, so show it and let the user save it
      // before moving on.
      setSavedPath(secret_path)
      setBusy(false)
    } catch (e) {
      notifyError(errMessage(e))
      setBusy(false)
    }
  }

  const finishKeep = async () => {
    setBusy(true)
    try {
      await finishSetup()
      // Both 'domain' (just issued) and 'keep' (already issued) serve only on
      // the domain, so the IP URL would now mismatch the cert — redirect there.
      if (wizMode !== 'ip') redirect(currentSecret())
      else onDone()
    } catch (e) {
      notifyError(errMessage(e))
      setBusy(false)
    }
  }

  return (
    <AuthShell wide>
        <div className="flex flex-col gap-5">
          <h1 className="text-center text-base font-semibold text-ink">{t('wizard.title')}</h1>

          {/* Mode choice */}
          {mode === '' && (
            <div className="flex animate-fade-in flex-col gap-2.5">
              <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.chooseStart')}</p>
              <ChoiceRow
                checked={false}
                onSelect={() => setMode('new')}
                title={t('wizard.newServer')}
                hint={t('wizard.newServerHint')}
              />
              <ChoiceRow
                checked={false}
                onSelect={() => setMode('restore')}
                title={t('wizard.restoreFromBackup')}
                hint={t('wizard.restoreHint')}
              />
            </div>
          )}

          {/* Restore flow */}
          {mode === 'restore' && <RestoreFlow onBack={() => setMode('')} />}

          {/* Save the freshly generated secret path before leaving the wizard */}
          {mode === 'new' && savedPath && (
            <div className="flex animate-fade-in flex-col gap-4">
              <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.newPathIntro')}</p>
              <Code block copy>
                {`https://${finalHost || window.location.hostname}/${savedPath}/`}
              </Code>
              <p className="warning-tint rounded-lg px-3 py-2.5 text-xs leading-relaxed text-warning">
                {t('wizard.newPathWarn')}
              </p>
              <Button onClick={() => redirect(savedPath)}>{t('wizard.savedGoToNew')}</Button>
            </div>
          )}

          {/* New server wizard */}
          {mode === 'new' && !savedPath && (
            <>
              {/* Where you are, and everywhere you have been: a bar per step, the
                  passed ones and the current one in the accent. "Начало" counts —
                  choosing new-or-restore is a step you took. */}
              <div className="flex gap-2">
                {STEP_KEYS.map((s, i) => (
                  <div key={s} className="flex min-w-0 flex-1 flex-col gap-1.5">
                    <span
                      className={cn(
                        'h-[3px] rounded-full',
                        i <= active + 1 ? 'bg-brand-600' : 'bg-gray-300',
                      )}
                    />
                    <span
                      className={cn(
                        'truncate text-[11px]',
                        i <= active + 1 ? 'text-ink' : 'text-ink-muted',
                      )}
                    >
                      {t(s)}
                    </span>
                  </div>
                ))}
              </div>

              {active === 0 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.passwordIntro')}</p>
                  <PasswordInput label={t('password.new')} value={password} onChange={setPassword} autoFocus />
                  <PasswordInput label={t('password.repeat')} value={confirm} onChange={setConfirm} />
                </div>
              )}

              {active === 1 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.timezoneIntro')}</p>
                  <Select
                    label={t('wizard.timezone')}
                    data={tzData}
                    value={timezone}
                    onChange={setTimezone}
                    searchable
                  />
                </div>
              )}

              {active === 2 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  {onDomainWithCert ? (
                    <>
                      <p className="text-xs leading-relaxed text-ink-muted">
                        {t('wizard.onDomainWithCert', { host: curHost })}
                        {cert ? t('wizard.certDaysLeft', { days: cert.days_left }) : ''}
                        {t('wizard.keepOrChange')}
                      </p>
                      <ChoiceRow
                        checked={wizMode === 'keep'}
                        onSelect={() => setWizMode('keep')}
                        title={t('wizard.keepDomain', { host: curHost })}
                      />
                      <ChoiceRow
                        checked={wizMode === 'domain'}
                        onSelect={() => setWizMode('domain')}
                        title={t('wizard.changeDomainOrIp')}
                      />
                    </>
                  ) : curIsDomain ? (
                    <>
                      <p className="warning-tint rounded-lg px-3 py-2.5 text-xs leading-relaxed text-warning">
                        {t('wizard.onDomainTempCert', { host: curHost })}
                      </p>
                      <ChoiceRow
                        checked={wizMode === 'domain'}
                        onSelect={() => setWizMode('domain')}
                        title={t('wizard.issueCertFor', { host: curHost })}
                      />
                      <ChoiceRow
                        checked={wizMode === 'keep'}
                        onSelect={() => setWizMode('keep')}
                        title={t('wizard.keepTempCert')}
                      />
                    </>
                  ) : (
                    <>
                      <p
                        className={cn(
                          'rounded-lg px-3 py-2.5 text-xs leading-relaxed',
                          certValid
                            ? 'accent-tint text-accent'
                            : 'warning-tint text-warning',
                        )}
                      >
                        {certValid ? t('wizard.onIp') : t('wizard.onIpTempCert')}
                      </p>
                      <ChoiceRow
                        checked={wizMode === 'ip'}
                        onSelect={() => setWizMode('ip')}
                        title={certValid ? t('wizard.stayOnIp') : t('wizard.stayOnIpTemp')}
                      />
                      <ChoiceRow
                        checked={wizMode === 'domain'}
                        onSelect={() => setWizMode('domain')}
                        title={t('wizard.moveToDomain')}
                      />
                    </>
                  )}
                  {wizMode === 'domain' && (
                    <>
                      <div>
                        <TextInput
                          label={isZeroSSL ? t('wizard.domain') : t('wizard.domainOrIp')}
                          placeholder={isZeroSSL ? 'vpn.example.com' : t('wizard.domainOrIpPlaceholder')}
                          value={domain}
                          onChange={setDomain}
                        />
                        {targetErr && (
                          <p className="mt-1 text-xs text-danger">
                            {isZeroSSL
                              ? t('wizard.errDomainOnly')
                              : t('wizard.errBadTarget')}
                          </p>
                        )}
                      </div>
                      <div>
                        <TextInput
                          label={isZeroSSL ? t('wizard.emailRequired') : t('wizard.emailOptional')}
                          placeholder="you@example.com"
                          value={email}
                          onChange={setEmail}
                        />
                        {emailErr && (
                          <p className="mt-1 text-xs text-danger">{t('wizard.errBadEmail')}</p>
                        )}
                      </div>
                      <Select
                        label={t('wizard.certAuthority')}
                        value={provider}
                        onChange={setProvider}
                        data={[
                          { value: 'letsencrypt', label: "Let's Encrypt" },
                          { value: 'zerossl', label: 'ZeroSSL' },
                        ]}
                      />
                      {isZeroSSL ? (
                        <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.zerosslNote')}</p>
                      ) : (
                        <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.letsencryptNote')}</p>
                      )}
                      <p className="text-xs text-ink-muted">{t('wizard.acmeRequirements')}</p>
                    </>
                  )}
                </div>
              )}

              {active === 3 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-xs leading-relaxed text-ink-muted">{t('wizard.pathIntro')}</p>
                  <ChoiceRow
                    checked={pathMode === 'generate'}
                    onSelect={() => setPathMode('generate')}
                    title={t('wizard.generatePath')}
                    hint={t('wizard.generatePathHint')}
                  />
                  <ChoiceRow
                    checked={pathMode === 'keep'}
                    onSelect={() => setPathMode('keep')}
                    title={t('wizard.keepPath')}
                  >
                    <Code block className="mt-1.5">
                      /{currentSecret()}/
                    </Code>
                  </ChoiceRow>
                  {/* Before "Завершить", not after it: rotating the path is the one
                      step of this wizard that cannot be undone, and the warning is
                      no use on the screen that already did it. */}
                  {pathMode === 'generate' && (
                    <p className="warning-tint rounded-lg px-3 py-2.5 text-xs leading-relaxed text-warning">
                      {t('wizard.newPathWarn')}
                    </p>
                  )}
                </div>
              )}

              <div className="flex items-center justify-between">
                <Button
                  variant="outline"
                  color="gray"
                  disabled={busy}
                  onClick={() => (active === 0 ? setMode('') : setActive((s) => Math.max(0, s - 1)))}
                >
                  {t('common.back')}
                </Button>
                {active === 0 && (
                  <Button loading={busy} onClick={savePassword}>
                    {t('common.next')}
                  </Button>
                )}
                {active === 1 && (
                  <Button loading={busy} onClick={saveTimezone}>
                    {t('common.next')}
                  </Button>
                )}
                {active === 2 && (
                  <Button
                    loading={busy}
                    disabled={wizMode === 'domain' && domainInvalid}
                    onClick={advanceAddress}
                  >
                    {wizMode === 'domain' ? t('wizard.getCert') : t('common.next')}
                  </Button>
                )}
                {active === 3 && (
                  <Button
                    loading={busy}
                    onClick={pathMode === 'generate' ? finishGenerate : finishKeep}
                  >
                    {t('wizard.finish')}
                  </Button>
                )}
              </div>
            </>
          )}
        </div>
    </AuthShell>
  )
}
