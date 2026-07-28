import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { login } from './api'
import { LangPills } from './LangSwitch'
import { BrandLogo } from './Logo'
import { notifyError } from './notify'
import { Button, Card, PasswordInput, TextInput } from './ui'

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
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      await login(username, password)
      onSuccess()
    } catch {
      notifyError(t('login.badCredentials'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      {/* The picker sits outside the card: an admin who can't read the form has
          nowhere else to reach it from — there is no account menu yet. */}
      <LangPills className="fixed right-3 top-3" />
      <Card className="w-full max-w-sm animate-fade-in-up p-6">
        <form onSubmit={submit} className="flex flex-col gap-3">
          <div className="mb-1 flex justify-center">
            <BrandLogo size={32} />
          </div>
          <TextInput
            label={t('login.username')}
            value={username}
            onChange={setUsername}
            autoFocus
          />
          <PasswordInput
            label={t('login.password')}
            value={password}
            onChange={setPassword}
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
      </Card>
    </div>
  )
}
