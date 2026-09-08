import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getNodeTraffic, type NodeTraffic } from './api'
import { ShareBar } from './charts'
import { fmtBytes } from './format'
import { Panel } from './ui'

// NodeTrafficSplit shows which server carried a period's traffic, under the chart
// that shows the total. Used by both the stats page (everyone) and the user card
// (one person), which differ only by user_id.
//
// It renders nothing at all on a single-server install: with only the panel's own
// node the split repeats the number above it. Same when the period has no traffic —
// a row of zeroes answers nothing.
export function NodeTrafficSplit({
  userId,
  from,
  to,
  title,
}: {
  userId?: number
  from: string
  to: string
  // With a title it stands as its own section (the statistics screen); without one
  // it is a block inside the card that already introduced it (the user card).
  title?: string
}) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<NodeTraffic[]>([])

  useEffect(() => {
    let alive = true // guard against an out-of-order response after a range switch
    getNodeTraffic({ user_id: userId, from, to })
      .then((d) => alive && setRows(d))
      .catch(() => alive && setRows([]))
    return () => {
      alive = false
    }
  }, [userId, from, to])

  if (rows.length < 2) return null

  const total = rows.reduce((sum, r) => sum + r.up + r.down, 0)
  const max = rows.reduce((m, r) => Math.max(m, r.up + r.down), 0)

  const list = (
    <div className="flex flex-col gap-2">
      {rows.map((r) => {
        const sum = r.up + r.down
        return (
          <ShareBar
            key={r.node_id}
            label={r.name}
            percent={max > 0 ? (sum / max) * 100 : 0}
            value={fmtBytes(sum)}
            title={`${r.name} · ↓ ${fmtBytes(r.down)} · ↑ ${fmtBytes(r.up)} · ${
              total > 0 ? Math.round((sum / total) * 100) : 0
            }%`}
          />
        )
      })}
    </div>
  )

  if (title) return <Panel title={title} pad>{list}</Panel>
  return (
    <div className="mt-4">
      <div className="mb-2 text-xs font-medium text-ink-muted">{t('stats.byServer')}</div>
      {list}
    </div>
  )
}
