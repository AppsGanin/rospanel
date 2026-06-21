import { useState, type FormEvent } from 'react'
import { login } from './api'
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
      notifyError('Неверный логин или пароль')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-sm animate-fade-in-up p-6">
        <form onSubmit={submit} className="flex flex-col gap-3">
          <div className="mb-1 flex justify-center">
            <BrandLogo size={32} />
          </div>
          <TextInput label="Логин" value={username} onChange={setUsername} autoFocus />
          <PasswordInput label="Пароль" value={password} onChange={setPassword} />
          <Button type="submit" loading={busy} fullWidth>
            Войти
          </Button>
          <div className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-xs text-ink-muted">
            <button
              type="button"
              onClick={onShowAgreement}
              className="transition hover:text-brand-600"
            >
              Пользовательское соглашение
            </button>
            <button
              type="button"
              onClick={onShowDonate}
              className="transition hover:text-brand-600"
            >
              Пожертвования
            </button>
          </div>
        </form>
      </Card>
    </div>
  )
}
