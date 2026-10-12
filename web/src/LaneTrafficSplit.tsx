import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getLaneTraffic, type LaneTraffic } from './api'
import { ShareBar } from './charts'
import { fmtBytes } from './format'
import { Panel, Select } from './ui'

// LaneTrafficSplit shows which protocol carried a period's traffic — every server
// together, or one of them. Renders nothing while there is no protocol traffic yet.
export function LaneTrafficSplit({
  from,
  to,
  refresh,
}: {
  from: string
  to: string
  // Changing it reads the split again (the statistics screen's minute tick).
  refresh?: number
}) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<LaneTraffic[]>([])
  const [server, setServer] = useState('all')

  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh only asks for a re-read; the request does not use it
  useEffect(() => {
    let alive = true // guard against an out-of-order response after a range switch
    getLaneTraffic({ from, to })
      .then((d) => alive && setRows(d))
      .catch(() => alive && setRows([]))
    return () => {
      alive = false
    }
  }, [from, to, refresh])

  const servers = useMemo(() => {
    const seen = new Map<number, string>()
    for (const r of rows) seen.set(r.node_id, r.node_name)
    return [...seen].map(([id, name]) => ({ value: String(id), label: name }))
  }, [rows])

  // A server picked for another period may have no traffic in this one; with it gone
  // from the list the picker hides, so fall back to every server rather than show none.
  useEffect(() => {
    if (server !== 'all' && !servers.some((s) => s.value === server)) setServer('all')
  }, [server, servers])

  // All servers: one bar per protocol name, summed over the servers it runs on.
  const bars = useMemo(() => {
    const picked = server === 'all' ? rows : rows.filter((r) => String(r.node_id) === server)
    const by = new Map<string, { name: string; up: number; down: number }>()
    for (const r of picked) {
      const b = by.get(r.name) ?? { name: r.name, up: 0, down: 0 }
      b.up += r.up
      b.down += r.down
      by.set(r.name, b)
    }
    return [...by.values()].sort((a, b) => b.up + b.down - (a.up + a.down))
  }, [rows, server])

  if (rows.length === 0) return null

  const total = bars.reduce((sum, b) => sum + b.up + b.down, 0)
  const max = bars.reduce((m, b) => Math.max(m, b.up + b.down), 0)

  return (
    <Panel
      title={t('stats.byProtocol')}
      aside={
        servers.length > 1 && (
          <Select
            size="sm"
            value={server}
            onChange={setServer}
            data={[{ value: 'all', label: t('stats.allServers') }, ...servers]}
          />
        )
      }
      pad
    >
      <div className="flex flex-col gap-2">
        {bars.map((b) => {
          const sum = b.up + b.down
          return (
            <ShareBar
              key={b.name}
              label={b.name}
              percent={max > 0 ? (sum / max) * 100 : 0}
              value={fmtBytes(sum)}
              title={`${b.name} · ↓ ${fmtBytes(b.down)} · ↑ ${fmtBytes(b.up)} · ${
                total > 0 ? Math.round((sum / total) * 100) : 0
              }%`}
            />
          )
        })}
      </div>
    </Panel>
  )
}
