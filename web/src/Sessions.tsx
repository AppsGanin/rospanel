import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { listSessions, revokeOtherSessions, revokeSession, type AdminSession } from './api'
import { fmtLastSeen } from './format'
import { useAction } from './hooks'
import { currentLang } from './i18n'
import { notifySuccess } from './notify'
import { Badge, Button, useConfirm } from './ui'

// clientLabel reduces a User-Agent to what tells sessions apart — "Chrome · macOS",
// "Safari · iPhone" — since the header itself is a hundred characters of history
// nobody reads. Anything it does not recognise falls back to the raw string's first
// token so a scripted client is still visible as *something*.
export function clientLabel(ua: string): string {
  if (!ua) return ''
  const has = (s: string) => ua.includes(s)
  let browser = ''
  if (has('Edg/')) browser = 'Edge'
  else if (has('OPR/') || has('Opera')) browser = 'Opera'
  else if (has('YaBrowser')) browser = 'Yandex'
  else if (has('Firefox/')) browser = 'Firefox'
  else if (has('Chrome/') || has('CriOS/')) browser = 'Chrome'
  else if (has('Safari/')) browser = 'Safari'
  let os = ''
  if (has('iPhone') || has('iPad')) os = has('iPad') ? 'iPad' : 'iPhone'
  else if (has('Android')) os = 'Android'
  else if (has('Windows')) os = 'Windows'
  else if (has('Mac OS X') || has('Macintosh')) os = 'macOS'
  else if (has('CrOS')) os = 'ChromeOS'
  else if (has('Linux')) os = 'Linux'
  if (browser || os) return [browser, os].filter(Boolean).join(' · ')
  return ua.split(/[\s/]/)[0]
}

function fmtDate(unix: number): string {
  return unix ? new Date(unix * 1000).toLocaleDateString(currentLang()) : '—'
}

// Sessions is the admin's own list of open sessions, inside the account dialog next
// to the password and the second factor: all three answer "who can get into this
// account". The current session is marked and cannot be ended from here — that is
// what the logout button is for — and "sign out everywhere else" is one click,
// because that is the whole action when a cookie has gone somewhere it should not.
export function Sessions() {
  const { t } = useTranslation()
  const [list, setList] = useState<AdminSession[] | null>(null)
  const { busy, run } = useAction()
  const { confirm, confirmNode } = useConfirm()

  const reload = () =>
    listSessions()
      .then(setList)
      .catch(() => {})

  useEffect(() => {
    reload()
  }, [])

  const others = (list ?? []).filter((s) => !s.current)

  const revoke = (s: AdminSession) =>
    run(async () => {
      await revokeSession(s.id)
      notifySuccess(t('sessions.revoked'))
      await reload()
    })

  const revokeAll = async () => {
    const ok = await confirm({
      title: t('sessions.revokeOthersTitle'),
      body: t('sessions.revokeOthersBody', { count: others.length }),
      confirmLabel: t('sessions.revokeOthers'),
      danger: true,
    })
    if (!ok) return
    run(async () => {
      const r = await revokeOtherSessions()
      notifySuccess(t('sessions.revokedN', { count: r.revoked }))
      await reload()
    })
  }

  return (
    <div className="flex flex-col gap-2 border-t border-gray-100 pt-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium text-ink">{t('sessions.title')}</span>
        {others.length > 0 && (
          <Button size="sm" variant="light" color="red" loading={busy} onClick={revokeAll}>
            {t('sessions.revokeOthers')}
          </Button>
        )}
      </div>
      <p className="text-xs text-ink-muted">{t('sessions.hint')}</p>
      {list !== null && list.length === 0 && (
        <p className="text-xs text-ink-muted">{t('sessions.none')}</p>
      )}
      {(list ?? []).map((s) => (
        <div
          key={s.id}
          className="flex items-start justify-between gap-2 rounded-lg border border-gray-100 px-3 py-2 text-sm"
        >
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="truncate font-medium" title={s.user_agent}>
                {clientLabel(s.user_agent) || t('sessions.unknownClient')}
              </span>
              {s.current && (
                <Badge color="green" size="xs">
                  {t('sessions.thisDevice')}
                </Badge>
              )}
            </div>
            <div className="text-xs text-ink-muted">
              {s.ip || '—'} · {t('sessions.lastSeen', { when: fmtLastSeen(s.last_seen_at) })} ·{' '}
              {t('sessions.signedIn', { when: fmtDate(s.created_at) })}
            </div>
          </div>
          {!s.current && (
            <Button size="sm" variant="light" color="gray" disabled={busy} onClick={() => revoke(s)}>
              {t('sessions.revoke')}
            </Button>
          )}
        </div>
      ))}
      {confirmNode}
    </div>
  )
}
