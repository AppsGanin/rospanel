import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { updateCredentials } from './api'
import { useAction } from './hooks'
import { notifyError, notifySuccess } from './notify'
import { useStepUpDialog } from './stepup'
import { TwoFactor } from './TwoFactor'
import { Sessions } from './Sessions'
import { Button, Modal, PasswordInput, TextInput } from './ui'

export function Credentials({
  username,
  onClose,
  onUpdated,
}: {
  username: string
  onClose: () => void
  onUpdated: () => void
}) {
  const { t } = useTranslation()
  const [login, setLogin] = useState(username)
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const { busy, run } = useAction()
  const { ask, stepUpNode } = useStepUpDialog()

  const submit = async () => {
    const changingPassword = password.length > 0
    if (changingPassword && password.length < 8) {
      return notifyError(t('password.tooShort'))
    }
    if (changingPassword && password !== confirm) {
      return notifyError(t('password.mismatch'))
    }
    if (!login.trim() && !changingPassword) {
      return notifyError(t('creds.nothingToSave'))
    }
    const creds = await ask({
      title: t('nav.credentials'),
      body: changingPassword ? t('creds.stepUpPassword') : t('creds.stepUpLogin'),
      confirmLabel: t('common.save'),
    })
    if (!creds) return
    run(async () => {
      // Send the login only if it changed; password only if entered. The current
      // password re-authenticates the change server-side.
      const newLogin = login.trim() && login.trim() !== username ? login.trim() : ''
      await updateCredentials(newLogin, password, creds.password)
      notifySuccess(t('creds.updated'))
      onUpdated() // refresh the header username immediately
      onClose()
    })
  }

  return (
    <Modal open onClose={onClose} title={t('nav.credentials')}>
      <div className="flex flex-col gap-3">
        <TextInput
          label={t('login.username')}
          value={login}
          onChange={setLogin}
          autoFocus
        />
        <PasswordInput
          label={t('password.new')}
          placeholder={t('creds.leaveEmpty')}
          value={password}
          onChange={setPassword}
        />
        {password.length > 0 && (
          <PasswordInput
            label={t('password.repeat')}
            value={confirm}
            onChange={setConfirm}
          />
        )}
        <Button loading={busy} onClick={submit}>
          {t('common.save')}
        </Button>
        {/* The second factor lives in the same dialog as the password: both answer
            "how do I get into this account", and an operator hardening their access
            should not have to find two screens. It asks for the password itself. */}
        <TwoFactor />
        <Sessions />
      </div>
      {stepUpNode}
    </Modal>
  )
}
