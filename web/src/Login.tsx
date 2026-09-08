import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, login } from './api'
import { AuthShell } from './AuthShell'
import { errMessage, notifyError } from './notify'
import { Button, Modal, PasswordInput, TextInput } from './ui'

export function Login({
  onSuccess,
  onShowAgreement,
  onShowDonate,
}: {
  onSuccess: () => void
  onShowAgreement: () => void
  onShowDonate: () => void
}) {
  const { t } = useTranslation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  // needCode appears only after the panel has said this account has a second factor,
  // which it does only once the password is already right — so the field itself never
  // tells an outsider whether an account exists or is protected.
  const [needCode, setNeedCode] = useState(false)
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      await login(username, password, needCode ? code : undefined)
      onSuccess()
    } catch (err) {
      const c = err instanceof ApiError ? err.code : undefined
      if (c === 'err.totpRequired') {
        setNeedCode(true)
        setCode('')
      } else if (c === 'err.totpInvalid') {
        // Stay on the code step: the password is fine, only this code was not.
        setNeedCode(true)
        setCode('')
        notifyError(t('login.badCode'))
      } else if (!c || c === 'err.badCredentials') {
        setNeedCode(false)
        notifyError(t('login.badCredentials'))
      } else {
        // Anything the panel named for itself — the lockout above all — keeps its own
        // wording and its place in the form. Telling someone who is throttled that
        // their password is wrong sends them hunting for a problem they don't have.
        notifyError(errMessage(err))
      }
    } finally {
      setBusy(false)
    }
  }

  // Leaving the code step goes back to the password form rather than half-way: the
  // session is not established until the code lands, so there is nothing to keep.
  const cancelCode = () => {
    setNeedCode(false)
    setCode('')
  }

  return (
    <AuthShell>
      <form onSubmit={submit} className="flex flex-col gap-3">
        {/* name + autoComplete are what a password manager keys on. Without them
            1Password and Chrome fill the admin login unreliably, which is the one
            form where that costs the operator real time. */}
        <TextInput
          label={t('login.username')}
          value={username}
          onChange={setUsername}
          name="username"
          autoComplete="username"
          autoFocus
        />
        <PasswordInput
          label={t('login.password')}
          value={password}
          onChange={setPassword}
          name="password"
          autoComplete="current-password"
        />
        <Button type="submit" loading={busy} fullWidth>
          {t('login.submit')}
        </Button>
        <div className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-xs text-ink-muted">
          <button
            type="button"
            onClick={onShowAgreement}
            className="transition hover:text-accent"
          >
            {t('nav.agreement')}
          </button>
          <button
            type="button"
            onClick={onShowDonate}
            className="transition hover:text-accent"
          >
            {t('nav.donate')}
          </button>
        </div>
      </form>

      {/* The second factor is its own question, asked once the password is already
          accepted — a dialog rather than a field that appears under the form the
          reader has just finished with. */}
      <Modal open={needCode} onClose={cancelCode} title={t('totp.title')}>
        <form onSubmit={submit} className="flex flex-col gap-3">
          <p className="text-xs leading-relaxed text-ink-muted">
            {t('login.codeIntro')}
          </p>
          <TextInput
            label={t('login.code')}
            value={code}
            onChange={(v) => setCode(v.replace(/\D/g, '').slice(0, 6))}
            placeholder="000000"
            name="code"
            autoComplete="one-time-code"
            inputMode="numeric"
            autoFocus
            mono
          />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="light" color="gray" onClick={cancelCode}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" loading={busy} disabled={code.length < 6}>
              {t('login.submit')}
            </Button>
          </div>
        </form>
      </Modal>
    </AuthShell>
  )
}
