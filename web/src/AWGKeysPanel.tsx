import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { addUserAWGKey, deleteUserAWGKey, getUserAWGKeys, type AWGKeyList } from './api'
import { fmtStamp } from './format'
import { errMessage, notifyError } from './notify'
import { Button, IconButton, IconClose, Mono, Panel, ReadOnly, TextInput, useConfirm } from './ui'

// AWGKeysPanel is the user card's AmneziaWG keys: one per device beyond the main one,
// so two devices on the account do not knock each other off the tunnel. The owner
// picks a key on the subscription page; a client that sends an HWID gets its own.
export function AWGKeysPanel({ userId, userName, canManage }: { userId: number; userName: string; canManage: boolean }) {
  const { t } = useTranslation()
  const { confirm, confirmNode } = useConfirm()
  const [list, setList] = useState<AWGKeyList | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    () =>
      getUserAWGKeys(userId)
        .then(setList)
        .catch(() => {}),
    [userId],
  )
  useEffect(() => {
    setList(null)
    load()
  }, [load])

  if (!list?.enabled) return null
  const count = list.keys.filter((k) => k.active).length + 1
  const canAdd = count < list.limit

  const add = async () => {
    setBusy(true)
    try {
      await addUserAWGKey(userId, name)
      setName('')
      await load()
    } catch (e) {
      notifyError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const remove = async (id: number, label: string) => {
    const ok = await confirm({
      title: t('awgKeys.deleteTitle'),
      body: t('awgKeys.deleteBody', { name: label, user: userName }),
      confirmLabel: t('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await deleteUserAWGKey(userId, id)
      await load()
    } catch (e) {
      notifyError(errMessage(e))
    }
  }

  return (
    <ReadOnly when={!canManage}>
      {confirmNode}
      <Panel
        title={t('awgKeys.title')}
        aside={<span className="text-xs text-ink-muted">{t('awgKeys.count', { count, limit: list.limit })}</span>}
      >
        <div className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5">
          <span className="text-sm text-ink">{t('awgKeys.main')}</span>
        </div>
        {list.keys.map((k) => {
          const label = k.name || `#${k.id}`
          return (
            <div
              key={k.id}
              className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0"
            >
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-sm text-ink">{label}</span>
                <span className="truncate text-xs text-ink-muted">
                  {k.hwid ? t('awgKeys.byClient') : t('awgKeys.byHand')} · {fmtStamp(k.created_at)}
                </span>
                {!k.active && <span className="text-xs text-danger">{t('awgKeys.overLimit')}</span>}
              </span>
              <span className="flex shrink-0 items-center gap-3">
                <Mono className="text-[11px] text-ink-muted">{k.addr}</Mono>
                <IconButton color="red" title={t('common.delete')} onClick={() => remove(k.id, label)}>
                  <IconClose size={16} />
                </IconButton>
              </span>
            </div>
          )
        })}
        <div className="flex flex-col gap-2 border-t border-gray-100 px-3.5 py-2.5 sm:flex-row sm:items-center">
          {canAdd ? (
            <>
              {/* Both the field and the button carry the same height: an input's border
                  makes it a couple of pixels taller than a filled button of the same
                  padding, and the pair reads as mismatched controls. */}
              <TextInput
                value={name}
                onChange={setName}
                placeholder={t('awgKeys.namePlaceholder')}
                ariaLabel={t('awgKeys.namePlaceholder')}
                className="h-9 min-w-0 flex-1"
              />
              <Button size="sm" className="h-9 shrink-0" onClick={add} loading={busy}>
                {t('awgKeys.add')}
              </Button>
            </>
          ) : (
            <span className="text-xs text-ink-muted">{t('awgKeys.full')}</span>
          )}
        </div>
        <p className="px-3.5 pb-3 text-xs text-ink-muted">{t('awgKeys.hint')}</p>
      </Panel>
    </ReadOnly>
  )
}
