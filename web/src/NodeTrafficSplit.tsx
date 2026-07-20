import { useEffect, useState } from 'react'
import { getNodeTraffic, type NodeTraffic } from './api'
import { fmtBytes } from './format'

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
}: {
  userId?: number
  from: string
  to: string
}) {
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

  return (
    <div className="mt-4">
      <div className="mb-2 text-xs font-medium text-ink-muted">По серверам</div>
      <div className="flex flex-col gap-1.5">
        {rows.map((r) => {
          const share = total > 0 ? Math.round(((r.up + r.down) / total) * 100) : 0
          return (
            <div key={r.node_id} className="flex items-center gap-3 text-sm">
              <span className="min-w-0 flex-1 truncate" title={r.name}>
                {r.name}
              </span>
              {/* A share bar makes "which server carries this person" readable at a
                  glance; the bytes stay for when the exact number matters. */}
              <span className="hidden h-1.5 w-24 shrink-0 overflow-hidden rounded-full bg-gray-100 sm:block">
                <span
                  className="block h-full rounded-full bg-brand-500"
                  style={{ width: `${share}%` }}
                />
              </span>
              <span className="w-10 shrink-0 text-right tabular-nums text-ink-muted">
                {share}%
              </span>
              <span className="w-20 shrink-0 text-right tabular-nums" title="Принято">
                ↓ {fmtBytes(r.down)}
              </span>
              <span className="w-20 shrink-0 text-right tabular-nums text-ink-muted" title="Отдано">
                ↑ {fmtBytes(r.up)}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
