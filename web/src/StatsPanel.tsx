import { useCallback, useEffect, useState } from 'react'
import {
  getStatsByUser,
  getStatsSeries,
  resetStats,
  type DailyPoint,
  type UserTotal,
} from './api'
import { fmtBytes, localDay, RANGES } from './format'
import { useAction } from './hooks'
import { useIsAdmin } from './role'
import { TrafficArea, TrafficDonut } from './charts'
import { Button, Card, Skeleton, SegmentedControl, useConfirm } from './ui'

const PALETTE = [
  '#2566f5', '#0d9488', '#9333ea', '#f97316', '#ef4444',
  '#06b6d4', '#65a30d', '#ec4899', '#4f46e5', '#eab308',
]

export function StatsPanel() {
  const isAdmin = useIsAdmin()
  const [range, setRange] = useState('30')
  const [series, setSeries] = useState<DailyPoint[]>([])
  const [totals, setTotals] = useState<UserTotal[]>([])
  const [loaded, setLoaded] = useState(false)
  const { busy, run } = useAction()
  const { confirm, confirmNode } = useConfirm()

  const load = useCallback(() => {
    const to = localDay(0)
    const from = localDay(Number(range) - 1)
    Promise.all([
      getStatsSeries({ from, to }).then(setSeries),
      getStatsByUser(from, to).then(setTotals),
    ])
      .catch(() => {})
      .finally(() => setLoaded(true))
  }, [range])

  useEffect(() => {
    load()
  }, [load])

  const doReset = async () => {
    const ok = await confirm({
      title: 'Очистить статистику?',
      body: 'Вся накопленная статистика трафика будет удалена. Действие необратимо.',
      confirmLabel: 'Очистить',
      danger: true,
    })
    if (!ok) return
    run(async () => {
      await resetStats()
      load()
    })
  }

  const chartData = series.map((p) => ({ day: p.day.slice(5), up: p.up, down: p.down }))
  const sumUp = totals.reduce((a, t) => a + t.up, 0)
  const sumDown = totals.reduce((a, t) => a + t.down, 0)

  const active = totals.filter((t) => t.up + t.down > 0)
  const colorById: Record<number, string> = {}
  active.forEach((t, i) => {
    colorById[t.user_id] = PALETTE[i % PALETTE.length]
  })
  const pieData = active.map((t) => ({
    name: t.name,
    value: t.up + t.down,
    color: colorById[t.user_id],
  }))

  if (!loaded) return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <Skeleton className="h-9 w-64 rounded-full" />
        <Skeleton className="h-8 w-32 rounded-lg" />
      </div>
      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between">
          <Skeleton className="h-5 w-28" />
          <Skeleton className="h-4 w-36" />
        </div>
        <Skeleton className="h-48 w-full rounded-lg" />
      </Card>
      <Card className="p-4">
        <Skeleton className="mb-3 h-5 w-24" />
        <div className="flex items-center gap-6">
          <Skeleton className="h-36 w-36 rounded-full" />
          <div className="flex flex-col gap-2 flex-1">
            {[...Array(4)].map((_, i) => (
              <div key={i} className="flex items-center gap-2">
                <Skeleton className="h-3 w-3 rounded-full" />
                <Skeleton className="h-3 flex-1" />
              </div>
            ))}
          </div>
        </div>
      </Card>
    </div>
  )

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl value={range} onChange={setRange} data={RANGES} />
        {/* Reading the numbers is the operator's job; wiping them is not. */}
        {isAdmin && (
          <Button color="red" variant="light" loading={busy} onClick={doReset}>
            Сбросить статистику
          </Button>
        )}
      </div>

      <Card className="p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="font-bold">Трафик по дням</h3>
          <p className="text-sm text-ink-muted">
            Σ ↓ {fmtBytes(sumDown)} · ↑ {fmtBytes(sumUp)}
          </p>
        </div>
        {chartData.length === 0 ? (
          <p className="py-10 text-center text-ink-muted">Нет данных за выбранный период</p>
        ) : (
          <TrafficArea data={chartData} fmt={fmtBytes} />
        )}
      </Card>

      <Card className="p-4">
        <h3 className="mb-3 font-bold">Доля трафика по пользователям</h3>
        <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
          <div className="flex items-center justify-center">
            {pieData.length > 0 ? (
              <TrafficDonut data={pieData} fmt={fmtBytes} centerLabel={fmtBytes(sumUp + sumDown)} />
            ) : (
              <p className="py-10 text-ink-muted">Нет данных</p>
            )}
          </div>
          <div className="max-h-80 overflow-auto">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-white">
                <tr className="border-b border-gray-200 text-left text-ink-muted">
                  <th className="py-2 pr-2 font-medium">Пользователь</th>
                  <th className="py-2 pr-2 font-medium">↓ Принято</th>
                  <th className="py-2 pr-2 font-medium">↑ Отдано</th>
                  <th className="py-2 font-medium">Всего</th>
                </tr>
              </thead>
              <tbody>
                {totals.map((t) => (
                  <tr key={t.user_id} className="border-b border-gray-100">
                    <td className="py-2 pr-2">
                      <span
                        className="mr-2 inline-block h-2.5 w-2.5 rounded-full align-middle"
                        style={{ background: colorById[t.user_id] || 'var(--color-gray-300)' }}
                      />
                      {t.name}
                    </td>
                    <td className="py-2 pr-2">{fmtBytes(t.down)}</td>
                    <td className="py-2 pr-2">{fmtBytes(t.up)}</td>
                    <td className="py-2">{fmtBytes(t.up + t.down)}</td>
                  </tr>
                ))}
                {totals.length === 0 && (
                  <tr>
                    <td colSpan={4} className="py-4 text-center text-ink-muted">
                      Нет данных
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </Card>
      {confirmNode}
    </div>
  )
}
