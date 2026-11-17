import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { type CascadeTraffic, getCascadeTraffic } from './api'
import { ShareBar } from './charts'
import { fmtBytes } from './format'
import { Panel } from './ui'

// CascadeTrafficSplit shows what each entry server sent through each exit server's
// cascade bridge, as the exit counted it. Renders nothing while no cascade carried any.
export function CascadeTrafficSplit({
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
  const [rows, setRows] = useState<CascadeTraffic[]>([])

  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh only asks for a re-read; the request does not use it
  useEffect(() => {
    let alive = true // guard against an out-of-order response after a range switch
    getCascadeTraffic({ from, to })
      .then((d) => alive && setRows(d))
      .catch(() => alive && setRows([]))
    return () => {
      alive = false
    }
  }, [from, to, refresh])

  if (rows.length === 0) return null

  const max = rows.reduce((m, r) => Math.max(m, r.up + r.down), 0)

  return (
    <Panel title={t('stats.byCascade')} aside={<span className="text-xs text-ink-muted">{t('stats.byCascadeHint')}</span>} pad>
      <div className="flex flex-col gap-2">
        {rows.map((r) => {
          const sum = r.up + r.down
          const label = `${r.entry_name} → ${r.exit_name}`
          return (
            <ShareBar
              key={`${r.exit_id}-${r.entry_id}`}
              label={label}
              percent={max > 0 ? (sum / max) * 100 : 0}
              value={fmtBytes(sum)}
              title={`${label} · ↓ ${fmtBytes(r.down)} · ↑ ${fmtBytes(r.up)}`}
            />
          )
        })}
      </div>
    </Panel>
  )
}
