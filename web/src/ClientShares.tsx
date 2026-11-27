import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { type ClientShare, getClientShares } from './api'
import { ShareBar } from './charts'
import { Panel, SegmentedControl } from './ui'

// ClientShares shows which apps, and which platforms, the users who refreshed their
// subscription in the period use. Renders nothing until anyone has.
export function ClientShares({ from, to, refresh }: { from: string; to: string; refresh?: number }) {
  const { t } = useTranslation()
  const [data, setData] = useState<{ apps: ClientShare[]; platforms: ClientShare[]; users: number }>({
    apps: [],
    platforms: [],
    users: 0,
  })
  const [view, setView] = useState<'apps' | 'platforms'>('apps')

  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh only asks for a re-read; the request does not use it
  useEffect(() => {
    let alive = true
    getClientShares({ from, to })
      .then((d) => alive && setData(d))
      .catch(() => alive && setData({ apps: [], platforms: [], users: 0 }))
    return () => {
      alive = false
    }
  }, [from, to, refresh])

  if (data.apps.length === 0) return null
  const rows = data[view]
  const max = rows.reduce((m, r) => Math.max(m, r.users), 0)
  // A user on two apps counts under both, so the share is of the users seen.
  const seen = data.users
  const unnamed = view === 'apps' ? t('stats.unknownClient') : t('stats.unknownPlatform')

  return (
    <Panel
      title={t('stats.clients')}
      aside={
        <SegmentedControl
          size="xs"
          nav
          value={view}
          onChange={(v) => setView(v as 'apps' | 'platforms')}
          data={[
            { value: 'apps', label: t('stats.clientApps') },
            { value: 'platforms', label: t('stats.clientPlatforms') },
          ]}
        />
      }
      pad
    >
      <div className="flex flex-col gap-2">
        {rows.map((r) => {
          const label = r.name && r.name !== 'unknown' ? r.name : unnamed
          return (
            <ShareBar
              key={r.name}
              label={label}
              percent={max > 0 ? (r.users / max) * 100 : 0}
              value={String(r.users)}
              title={`${label} · ${seen > 0 ? Math.round((r.users / seen) * 100) : 0}%`}
            />
          )
        })}
      </div>
    </Panel>
  )
}
