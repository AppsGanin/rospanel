import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getUserClients, getUserLanes, type SubClient, type UserLane } from './api'
import { ShareBar } from './charts'
import { fmtDuration } from './format'

// useUserClients loads the apps a user fetched the subscription with, whether or
// not HWID binding is on, and reads them again every five minutes.
export function useUserClients(userId: number): SubClient[] {
  const [list, setList] = useState<SubClient[]>([])
  useEffect(() => {
    setList([])
    if (!userId) return
    let alive = true
    const load = () =>
      getUserClients(userId)
        .then((d) => alive && setList(d.clients))
        .catch(() => {})
    load()
    const id = setInterval(load, 300_000)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [userId])
  return list
}

// clientName is the app as it reads in a list — "Happ 6.4.0" — or the caller's word for
// an app the server could not name (it calls those "unknown", which is a sentinel, not
// a name to show).
export function clientName(c: SubClient, unknown: string): string {
  const raw = [c.app, c.version].filter(Boolean).join(' ')
  return !raw || c.app === 'unknown' ? unknown : raw
}

// UserLaneSplit shows how long a user was active on each protocol over the period,
// under the traffic chart. Renders nothing until the period has any.
export function UserLaneSplit({ userId, from, to }: { userId: number; from: string; to: string }) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<UserLane[]>([])

  useEffect(() => {
    let alive = true // guard against an out-of-order response after a range switch
    setRows([]) // the previous user's (or period's) bars must not sit under the new one
    getUserLanes(userId, { from, to })
      .then((d) => alive && setRows(d))
      .catch(() => alive && setRows([]))
    return () => {
      alive = false
    }
  }, [userId, from, to])

  // Lanes of different servers that share a name are one protocol to the reader.
  const by = new Map<string, number>()
  for (const r of rows) by.set(r.name, (by.get(r.name) ?? 0) + r.minutes)
  const bars = [...by].sort((a, b) => b[1] - a[1])
  if (bars.length === 0) return null
  const total = bars.reduce((sum, [, m]) => sum + m, 0)
  const max = bars[0][1]

  return (
    <div className="mt-4">
      <div className="mb-2 text-xs font-medium text-ink-muted" title={t('userDetail.byProtocolHint')}>
        {t('stats.byProtocol')}
      </div>
      <div className="flex flex-col gap-2">
        {bars.map(([name, minutes]) => (
          <ShareBar
            key={name}
            label={name}
            percent={max > 0 ? (minutes / max) * 100 : 0}
            value={fmtDuration(minutes * 60)}
            title={`${name} · ${fmtDuration(minutes * 60)} · ${
              total > 0 ? Math.round((minutes / total) * 100) : 0
            }%`}
          />
        ))}
      </div>
    </div>
  )
}
