import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { importUsers, inspectImport, type ImportCandidate, type ImportPreview } from './api'
import { fmtBytes, fmtExpire } from './format'
import { useAction } from './hooks'
import { td } from './i18n'
import { notifySuccess } from './notify'
import { Badge, Button, Code, Modal, TextInput, TD, THead, TR, TableShell } from './ui'

// ImportUsersModal moves users over from another panel in two steps: the file is
// uploaded and read (nothing written), then the rows the operator kept are
// created with the same UUIDs and passwords — so nobody has to re-add a server in
// their app. Users already here (by UUID) are pre-unticked and skipped server-side
// regardless, so the same file run twice cannot double anyone.
export function ImportUsersModal({
  open,
  onClose,
  onImported,
}: {
  open: boolean
  onClose: () => void
  onImported: () => void
}) {
  const { t } = useTranslation()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [preview, setPreview] = useState<ImportPreview | null>(null)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [tag, setTag] = useState('')
  const [failed, setFailed] = useState<{ name: string; code: string }[]>([])
  const { busy, run } = useAction()

  // Start over every time the dialog opens: a preview belongs to one file.
  useEffect(() => {
    if (!open) return
    setFile(null)
    setPreview(null)
    setPicked(new Set())
    setTag('')
    setFailed([])
  }, [open])

  const inspect = () => {
    if (!file) return
    run(async () => {
      const p = await inspectImport(file)
      setPreview(p)
      // Everything new is ticked; what is already here is not.
      setPicked(new Set(p.users.map((u, i) => (u.exists ? -1 : i)).filter((i) => i >= 0)))
      setTag(`import-${p.source}`)
      setFailed([])
    })
  }

  const users = preview?.users ?? []
  const newCount = useMemo(() => users.filter((u) => !u.exists).length, [users])
  const allNewPicked = newCount > 0 && users.every((u, i) => u.exists || picked.has(i))
  const toggle = (i: number, on: boolean) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (on) next.add(i)
      else next.delete(i)
      return next
    })
  const toggleAll = () =>
    setPicked(allNewPicked ? new Set() : new Set(users.map((u, i) => (u.exists ? -1 : i)).filter((i) => i >= 0)))

  const doImport = () => {
    if (!preview) return
    const chosen: ImportCandidate[] = users.filter((_, i) => picked.has(i))
    run(async () => {
      const res = await importUsers(preview.source, chosen, tag.trim() ? [tag.trim()] : [])
      notifySuccess(t('importUsers.done', { created: res.created, skipped: res.skipped, failed: res.failed.length }))
      onImported()
      if (res.failed.length === 0) {
        onClose()
        return
      }
      setFailed(res.failed)
    })
  }

  const issueBadge = (key: string, color: 'orange' | 'gray' | 'brand', label: string) => (
    <Badge key={key} color={color} size="xs" title={label}>
      {label}
    </Badge>
  )

  return (
    <Modal open={open} onClose={onClose} size="xl" title={t('importUsers.title')}>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-ink-muted">{t('importUsers.intro')}</p>
        <ul className="list-disc pl-5 text-xs text-ink-muted">
          <li>{t('importUsers.srcRosPanel')}</li>
          <li>{t('importUsers.srcMarzban')}</li>
          <li>{t('importUsers.srcXui')}</li>
        </ul>

        {!preview && (
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              ref={fileRef}
              type="file"
              accept=".db,.sqlite,.sqlite3,.json,application/json,application/x-sqlite3,application/octet-stream"
              onChange={(e) => setFile(e.currentTarget.files?.[0] ?? null)}
              className="block w-full text-sm text-ink file:mr-3 file:rounded-lg file:border-0 file:bg-gray-100 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-ink hover:file:bg-gray-200"
            />
            <Button loading={busy} disabled={!file} onClick={inspect} className="sm:shrink-0">
              {t('importUsers.inspect')}
            </Button>
          </div>
        )}

        {preview && (
          <>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <Badge color="brand">{preview.source}</Badge>
              <span>{t('importUsers.found', { total: users.length, fresh: newCount })}</span>
              <button
                type="button"
                className="ml-auto text-xs text-accent hover:underline"
                onClick={() => {
                  setPreview(null)
                  setFile(null)
                }}
              >
                {t('importUsers.anotherFile')}
              </button>
            </div>

            <div>
              <TextInput
                label={t('importUsers.tag')}
                value={tag}
                onChange={setTag}
                placeholder="import-marzban"
              />
              <p className="mt-1 text-xs text-ink-muted">{t('importUsers.tagHint')}</p>
            </div>

            {users.length > 0 && (
              <TableShell>
                <THead
                  cols={[
                    { srOnly: t('usersPanel.colSelect'), className: 'w-10' },
                    { label: t('usersPanel.colUser') },
                    { label: t('usersPanel.colTraffic'), className: 'hidden sm:table-cell' },
                    { label: t('usersPanel.colExpires'), className: 'hidden md:table-cell' },
                    { label: t('importUsers.colNotes') },
                  ]}
                />
                <tbody>
                  <tr>
                    <td colSpan={5} className="px-3 py-1.5 text-xs">
                      <button type="button" className="text-accent hover:underline" onClick={toggleAll}>
                        {allNewPicked ? t('importUsers.deselect') : t('importUsers.selectNew', { count: newCount })}
                      </button>
                    </td>
                  </tr>
                  {users.map((u, i) => (
                    <TR key={i} selected={picked.has(i)}>
                      <TD>
                        <input
                          type="checkbox"
                          disabled={u.exists}
                          checked={picked.has(i)}
                          onChange={(e) => toggle(i, e.currentTarget.checked)}
                          aria-label={u.name}
                        />
                      </TD>
                      <TD>
                        <div className="flex min-w-0 flex-col">
                          <span className={u.enabled ? 'font-medium text-ink' : 'font-medium text-ink-muted'}>
                            {u.name}
                          </span>
                          <Code className="w-fit text-ink-muted">{u.uuid.slice(0, 8)}…</Code>
                        </div>
                      </TD>
                      <TD className="hidden whitespace-nowrap sm:table-cell">
                        {fmtBytes(u.used_up + u.used_down)}
                        {u.data_limit > 0 ? ` / ${fmtBytes(u.data_limit)}` : ''}
                      </TD>
                      <TD className="hidden whitespace-nowrap text-ink-muted md:table-cell">
                        {u.expire_at > 0 ? fmtExpire(u.expire_at) : '—'}
                      </TD>
                      <TD>
                        <div className="flex flex-wrap gap-1">
                          {!u.enabled && issueBadge('off', 'gray', t('userDetail.off'))}
                          {u.exists && issueBadge('exists', 'brand', t('importUsers.issue.exists'))}
                          {u.name_taken && !u.exists && issueBadge('name', 'gray', t('importUsers.issue.name_taken'))}
                          {u.issues.map((k) => issueBadge(k, 'orange', td(`importUsers.issue.${k}`)))}
                          {u.device_limit > 0 &&
                            issueBadge('dev', 'gray', t('usersPanel.devicesShort', { active: 0, limit: u.device_limit }))}
                        </div>
                      </TD>
                    </TR>
                  ))}
                </tbody>
              </TableShell>
            )}

            {failed.length > 0 && (
              <div className="warning-tint rounded-lg px-3 py-2 text-xs text-warning">
                <p className="font-medium">{t('importUsers.failedTitle', { count: failed.length })}</p>
                <ul className="mt-1 list-disc pl-4">
                  {failed.map((f, i) => (
                    <li key={i}>
                      {f.name || '—'}: {td(f.code)}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <div className="flex justify-end gap-2">
              <Button variant="light" color="gray" onClick={onClose} disabled={busy}>
                {t('common.cancel')}
              </Button>
              <Button loading={busy} disabled={picked.size === 0} onClick={doImport}>
                {t('importUsers.import', { count: picked.size })}
              </Button>
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}
