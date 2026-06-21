import { useState } from 'react'
import { updateCredentials } from './api'
import { useAction } from './hooks'
import { notifyError, notifySuccess } from './notify'
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
  const [login, setLogin] = useState(username)
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const { busy, run } = useAction()

  const submit = async () => {
    const changingPassword = password.length > 0
    if (changingPassword && password.length < 8) {
      return notifyError('Пароль должен быть не короче 8 символов')
    }
    if (changingPassword && password !== confirm) {
      return notifyError('Пароли не совпадают')
    }
    if (!login.trim() && !changingPassword) {
      return notifyError('Нечего сохранять')
    }
    if (!current) {
      return notifyError('Введите текущий пароль для подтверждения')
    }
    run(async () => {
      // Send the login only if it changed; password only if entered. The current
      // password re-authenticates the change server-side.
      const newLogin = login.trim() && login.trim() !== username ? login.trim() : ''
      await updateCredentials(newLogin, password, current)
      notifySuccess('Учётные данные обновлены')
      onUpdated() // refresh the header username immediately
      onClose()
    })
  }

  return (
    <Modal open onClose={onClose} title="Учётные данные">
      <div className="flex flex-col gap-3">
        <TextInput label="Логин" value={login} onChange={setLogin} autoFocus />
        <PasswordInput
          label="Новый пароль"
          placeholder="оставьте пустым, чтобы не менять"
          value={password}
          onChange={setPassword}
        />
        {password.length > 0 && (
          <PasswordInput label="Повторите пароль" value={confirm} onChange={setConfirm} />
        )}
        <PasswordInput
          label="Текущий пароль"
          placeholder="для подтверждения изменений"
          value={current}
          onChange={setCurrent}
        />
        <Button loading={busy} onClick={submit}>
          Сохранить
        </Button>
      </div>
    </Modal>
  )
}
