import { useEffect, useMemo, useState } from 'react'
import { finishSetup, getTLS, regenSecret, setACME, setupPassword, setupTimezone } from './api'
import type { TLSStatus } from './api'
import { BrandLogo } from './Logo'
import { errMessage, notifyError } from './notify'
import { BACKUP_ACCEPT, ManifestCard, RestoreWaiting, useRestore, ValidationNote } from './restore'
import { browserTimezone, tzOptions } from './tz'
import { Button, Card, cn, Code, IconCheck, PasswordInput, Select, TextInput } from './ui'
import { isIP, isValidACMETarget, isValidEmail } from './validate'

const STEPS = ['Пароль', 'Время', 'Адрес', 'Путь панели']

function currentSecret(): string {
  return window.location.pathname.split('/').filter(Boolean)[0] || 'rospanel'
}

// ── Restore flow ─────────────────────────────────────────────────────────────

function RestoreFlow({ onBack }: { onBack: () => void }) {
  const { fileRef, file, inspection, manifest, inspecting, restoring, done, pick, restore } = useRestore()

  if (done) return <RestoreWaiting manifest={done} currentDomain={window.location.hostname} />

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-ink-muted">
        Выберите файл бэкапа (.tar.gz). Все текущие данные будут заменены, панель перезапустится.
      </p>
      <input
        ref={fileRef}
        type="file"
        accept={BACKUP_ACCEPT}
        className="hidden"
        onChange={(e) => pick(e.target.files?.[0] ?? null)}
      />
      <Button variant="light" color="gray" loading={inspecting} onClick={() => fileRef.current?.click()}>
        {file ? file.name : 'Выбрать файл…'}
      </Button>

      {manifest && <ManifestCard m={manifest} label="В бэкапе" />}
      {inspection && <ValidationNote inspection={inspection} />}

      <div className="flex items-center justify-between">
        <Button variant="outline" color="gray" onClick={onBack}>
          Назад
        </Button>
        {manifest && (
          <Button
            color="red"
            loading={restoring}
            disabled={!inspection?.valid}
            onClick={restore}
          >
            Восстановить и перезапустить
          </Button>
        )}
      </div>
    </div>
  )
}

// ── Main Wizard ───────────────────────────────────────────────────────────────

export function Wizard({ onDone }: { onDone: () => void }) {
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
  // (mirrors the settings TLS panel's "valid vs временный" distinction).
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
    if (password.length < 8) return notifyError('Пароль должен быть не короче 8 символов')
    if (password !== confirm) return notifyError('Пароли не совпадают')
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
        notifyError(errMessage(e, 'Не удалось получить сертификат'))
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
      redirect(secret_path)
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
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-xl animate-fade-in-up p-6 sm:p-8">
        <div className="flex flex-col gap-5">
          <div className="flex justify-center">
            <BrandLogo size={30} />
          </div>
          <h1 className="text-center text-lg font-bold">Первоначальная настройка</h1>

          {/* Mode choice */}
          {mode === '' && (
            <div className="flex animate-fade-in flex-col gap-3">
              <p className="text-sm text-ink-muted">Выберите, как начать работу с панелью.</p>
              <button
                className="flex flex-col gap-1 rounded-xl border-2 border-accent accent-tint p-4 text-left transition hover:border-brand-500"
                onClick={() => setMode('new')}
              >
                <span className="font-semibold text-ink">Новый сервер</span>
                <span className="text-sm text-ink-muted">
                  Настройте пароль, часовой пояс и домен с нуля.
                </span>
              </button>
              <button
                className="flex flex-col gap-1 rounded-xl border-2 border-gray-200 bg-gray-50 p-4 text-left transition hover:border-gray-400"
                onClick={() => setMode('restore')}
              >
                <span className="font-semibold text-ink">Восстановить из бэкапа</span>
                <span className="text-sm text-ink-muted">
                  Загрузите архив — пользователи и настройки будут восстановлены.
                </span>
              </button>
            </div>
          )}

          {/* Restore flow */}
          {mode === 'restore' && <RestoreFlow onBack={() => setMode('')} />}

          {/* New server wizard */}
          {mode === 'new' && (
            <>
              {/* Stepper header */}
              <div className="flex items-center">
                {STEPS.map((s, i) => (
                  <div key={s} className={cn('flex items-center', i < STEPS.length - 1 && 'flex-1')}>
                    <div className="flex items-center gap-2">
                      <span
                        className={cn(
                          'flex h-7 w-7 items-center justify-center rounded-full text-sm font-semibold',
                          i < active && 'bg-brand-600 text-onaccent',
                          i === active && 'bg-brand-600 text-onaccent',
                          i > active && 'bg-gray-200 text-gray-500',
                        )}
                      >
                        {i < active ? <IconCheck /> : i + 1}
                      </span>
                      <span
                        className={cn(
                          'hidden text-sm font-medium sm:block',
                          i <= active ? 'text-ink' : 'text-gray-400',
                        )}
                      >
                        {s}
                      </span>
                    </div>
                    {i < STEPS.length - 1 && (
                      <div
                        className={cn('mx-2 h-px flex-1', i < active ? 'bg-brand-500' : 'bg-gray-200')}
                      />
                    )}
                  </div>
                ))}
              </div>

              {active === 0 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-sm text-ink-muted">
                    Задайте новый пароль администратора вместо выданного при установке.
                  </p>
                  <PasswordInput label="Новый пароль" value={password} onChange={setPassword} autoFocus />
                  <PasswordInput label="Повторите пароль" value={confirm} onChange={setConfirm} />
                </div>
              )}

              {active === 1 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-sm text-ink-muted">
                    Используется для границы суток в статистике трафика.
                  </p>
                  <Select
                    label="Часовой пояс"
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
                      <p className="text-sm text-ink-muted">
                        Панель уже работает на домене{' '}
                        <span className="font-medium text-ink">{curHost}</span>
                        {cert ? `, сертификат действует ещё ${cert.days_left} дн.` : ''}. Можно
                        оставить как есть или сменить адрес.
                      </p>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'keep'}
                          onChange={() => setWizMode('keep')}
                          className="accent-brand-600"
                        />
                        Оставить домен {curHost}
                      </label>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'domain'}
                          onChange={() => setWizMode('domain')}
                          className="accent-brand-600"
                        />
                        Сменить домен или перейти на IP
                      </label>
                    </>
                  ) : curIsDomain ? (
                    <>
                      <p className="text-sm text-ink-muted">
                        Панель настроена на домен{' '}
                        <span className="font-medium text-ink">{curHost}</span>, но действующий
                        сертификат пока временный (самоподписанный) — настоящий ещё не выпущен (домен
                        не указывает на сервер, закрыт порт 80 или достигнут лимит выпуска).
                      </p>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'domain'}
                          onChange={() => setWizMode('domain')}
                          className="accent-brand-600"
                        />
                        Выпустить сертификат для {curHost}
                      </label>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'keep'}
                          onChange={() => setWizMode('keep')}
                          className="accent-brand-600"
                        />
                        Пока оставить временный сертификат
                      </label>
                    </>
                  ) : (
                    <>
                      <p className="text-sm text-ink-muted">
                        Панель сейчас работает по IP
                        {certValid ? '' : ' с временным самоподписанным сертификатом'}. Можно перейти
                        на домен и выпустить для него сертификат Let's Encrypt, либо остаться на IP.
                      </p>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'ip'}
                          onChange={() => setWizMode('ip')}
                          className="accent-brand-600"
                        />
                        {certValid
                          ? 'Остаться на IP (текущий сертификат)'
                          : 'Остаться на IP (временный сертификат)'}
                      </label>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name="mode"
                          checked={wizMode === 'domain'}
                          onChange={() => setWizMode('domain')}
                          className="accent-brand-600"
                        />
                        Перейти на домен
                      </label>
                    </>
                  )}
                  {wizMode === 'domain' && (
                    <>
                      <div>
                        <TextInput
                          label={isZeroSSL ? 'Домен' : 'Домен или IP'}
                          placeholder={isZeroSSL ? 'vpn.example.com' : 'vpn.example.com или 144.31.159.81'}
                          value={domain}
                          onChange={setDomain}
                        />
                        {targetErr && (
                          <p className="mt-1 text-xs text-danger">
                            {isZeroSSL
                              ? 'Введите домен (ZeroSSL не выдаёт сертификаты на IP).'
                              : 'Введите корректный домен или IP-адрес.'}
                          </p>
                        )}
                      </div>
                      <div>
                        <TextInput
                          label={isZeroSSL ? 'E-mail (обязательно для ZeroSSL)' : 'E-mail (необязательно)'}
                          placeholder="you@example.com"
                          value={email}
                          onChange={setEmail}
                        />
                        {emailErr && (
                          <p className="mt-1 text-xs text-danger">Введите корректный e-mail.</p>
                        )}
                      </div>
                      <Select
                        label="Центр сертификации"
                        value={provider}
                        onChange={setProvider}
                        data={[
                          { value: 'letsencrypt', label: "Let's Encrypt" },
                          { value: 'zerossl', label: 'ZeroSSL' },
                        ]}
                      />
                      {isZeroSSL ? (
                        <p className="text-sm text-ink-muted">
                          ZeroSSL поддерживает только домены. EAB-ключи будут получены автоматически
                          по указанному e-mail.
                        </p>
                      ) : (
                        <p className="text-sm text-ink-muted">
                          Let's Encrypt выдаёт сертификаты на домены и IP (на IP ~6 дней, продлеваются
                          автоматически).
                        </p>
                      )}
                      <p className="text-xs text-ink-muted">
                        Домен должен указывать на этот сервер, порт 80 открыт. Выпуск занимает
                        10–30 секунд.
                      </p>
                    </>
                  )}
                </div>
              )}

              {active === 3 && (
                <div className="flex animate-fade-in flex-col gap-3">
                  <p className="text-sm text-ink-muted">
                    Панель открывается по секретному пути. Рекомендуем сгенерировать случайный — так
                    панель сложнее обнаружить.
                  </p>
                  <label className="flex items-start gap-2 text-sm">
                    <input
                      type="radio"
                      name="pathmode"
                      checked={pathMode === 'generate'}
                      onChange={() => setPathMode('generate')}
                      className="mt-1 accent-brand-600"
                    />
                    <span>
                      <span className="font-medium text-ink">Сгенерировать новый случайный путь</span>
                      <span className="block text-xs text-ink-muted">
                        Рекомендуется. После смены вас перекинет на новый адрес.
                      </span>
                    </span>
                  </label>
                  <label className="flex items-start gap-2 text-sm">
                    <input
                      type="radio"
                      name="pathmode"
                      checked={pathMode === 'keep'}
                      onChange={() => setPathMode('keep')}
                      className="mt-1 accent-brand-600"
                    />
                    <span>
                      <span className="font-medium text-ink">Оставить текущий путь</span>
                      <Code block className="mt-1">
                        /{currentSecret()}/
                      </Code>
                    </span>
                  </label>
                </div>
              )}

              <div className="flex items-center justify-between">
                <Button
                  variant="outline"
                  color="gray"
                  disabled={busy}
                  onClick={() => (active === 0 ? setMode('') : setActive((s) => Math.max(0, s - 1)))}
                >
                  Назад
                </Button>
                {active === 0 && (
                  <Button loading={busy} onClick={savePassword}>
                    Далее
                  </Button>
                )}
                {active === 1 && (
                  <Button loading={busy} onClick={saveTimezone}>
                    Далее
                  </Button>
                )}
                {active === 2 && (
                  <Button
                    loading={busy}
                    disabled={wizMode === 'domain' && domainInvalid}
                    onClick={advanceAddress}
                  >
                    {wizMode === 'domain' ? 'Получить сертификат' : 'Далее'}
                  </Button>
                )}
                {active === 3 && (
                  <Button
                    loading={busy}
                    onClick={pathMode === 'generate' ? finishGenerate : finishKeep}
                  >
                    Завершить
                  </Button>
                )}
              </div>
            </>
          )}
        </div>
      </Card>
    </div>
  )
}
