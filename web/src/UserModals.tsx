import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { bulkUsers, renameUser, type User } from './api'
import { fmtTerm } from './format'
import { useAction } from './hooks'
import { notifySuccess } from './notify'
import { Button, Modal, TextInput } from './ui'

// RenameModal is the account's name, changed on purpose. It was an inline pencil in
// the drawer header; at 520px that header holds the name, the id and the close
// button, and an input had nowhere to grow.
export function RenameModal({
  user,
  open,
  onClose,
  onChanged,
}: {
  user: User
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(user.name)
  const { busy, run } = useAction()

  useEffect(() => {
    if (open) setDraft(user.name)
  }, [open, user.name])

  const save = () => {
    const name = draft.trim()
    if (!name || name === user.name) return onClose()
    run(async () => {
      await renameUser(user.id, name)
      onChanged()
      onClose()
    })
  }

  return (
    <Modal open={open} onClose={onClose} title={t('userDetail.rename')}>
      <div className="flex flex-col gap-4">
        <TextInput label={t('usersPanel.name')} value={draft} onChange={setDraft} autoFocus />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button loading={busy} disabled={!draft.trim()} onClick={save}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ExtendUserModal adds days to one account's expiry through the same server path the
// list's bulk action uses — one user and fifty must not behave differently.
export function ExtendUserModal({
  user,
  open,
  onClose,
  onChanged,
}: {
  user: User
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [days, setDays] = useState('30')
  const { busy, run } = useAction()
  const n = Math.floor(Number(days) || 0)

  return (
    <Modal open={open} onClose={onClose} title={t('usersPanel.extendTitle')}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-ink-muted">
          {t('userDetail.extendUserBody', { name: user.name, date: fmtTerm(user.expire_at, user.hold_seconds) })}
        </p>
        <div className="flex flex-wrap gap-2">
          {EXTEND_PRESETS.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={n === p ? 'filled' : 'light'}
              color="gray"
              onClick={() => setDays(String(p))}
            >
              {t('usersPanel.plusDays', { count: p })}
            </Button>
          ))}
        </div>
        <TextInput label={t('usersPanel.days')} type="number" value={days} onChange={setDays} />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            loading={busy}
            disabled={n <= 0}
            onClick={() =>
              run(async () => {
                await bulkUsers([user.id], 'extend', n)
                onChanged()
                notifySuccess(t('userDetail.extended', { count: n }))
                onClose()
              })
            }
          >
            {t('usersPanel.extendByDays', { count: n })}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// EXTEND_PRESETS mirrors the list's bulk dialog, so the same four choices are offered
// wherever a subscription is extended.
const EXTEND_PRESETS = [7, 30, 90, 180]
