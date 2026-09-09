import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getStatsByUser,
  getStatsSeries,
  resetStats,
  type DailyPoint,
  type UserTotal,
} from './api'
import { fmtBytes, localDay, ranges } from './format'
import { useAction, useShowMore } from './hooks'
import { useIsAdmin } from './role'
import { ShareBar, TrafficArea } from './charts'
import { AbuseList } from './AbuseList'
import { ConnectionCountries } from './CountryMap'
import { NodeTrafficSplit } from './NodeTrafficSplit'
import { BlockedList, ProbeList } from './SecurityLists'
import {
  Button,
  EmptyState,
  Mono,
  Panel,
  SegmentedControl,
  ShowMore,
  Skeleton,
  useConfirm,
} from './ui'

// How many users the share list opens with. The rest are one click away: the tail of
// a long install is a hundred accounts that used a megabyte each, and it answers
// nothing about who is carrying the traffic.
const SHARE_FIRST = 8

export function StatsPanel() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const [range, setRange] = useState('30')
  const [series, setSeries] = useState<DailyPoint[]>([])
  const [totals, setTotals] = useState<UserTotal[]>([])
  const [loaded, setLoaded] = useState(false)
  const { busy, run } = useAction()
  const { confirm, confirmNode } = useConfirm()

  const from = localDay(Number(range) - 1)
  const to = localDay(0)

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
      title: t('stats.resetTitle'),
      body: t('stats.resetBody'),
      confirmLabel: t('common.clear'),
      danger: true,
    })
    if (!ok) return
    run(async () => {
      await resetStats()
      load()
    })
  }

  // The chart takes the day without its year — the range picker above already says
  // which window this is, and "2026-06-12" under every tick is four characters of
  // noise per column.
  const chart = series.map((p) => ({ day: p.day.slice(5), up: p.up, down: p.down }))
  const sumDown = series.reduce((a, p) => a + p.down, 0)
  const sumUp = series.reduce((a, p) => a + p.up, 0)
  const sumDays = sumDown + sumUp

  // Sorted by what they spent, largest first — the list is read as a ranking, and the
  // server returns it in whatever order the query produced.
  const share = totals
    .map((u) => ({ ...u, total: u.up + u.down }))
    .filter((u) => u.total > 0)
    .sort((a, b) => b.total - a.total)
  const maxShare = share.length > 0 ? share[0].total : 0
  const page = useShowMore(share, { first: SHARE_FIRST, step: SHARE_FIRST, resetKey: range })

  if (!loaded)
    return (
      <div className="flex flex-col gap-3.5">
        <Skeleton className="h-8 w-64 rounded-lg" />
        <Panel title={t('stats.trafficByDay')} pad>
          <Skeleton className="h-32 w-full rounded-lg" />
        </Panel>
        <Panel title={t('stats.shareByUser')} pad>
          <div className="flex flex-col gap-2">
            {[...Array(5)].map((_, i) => (
              <Skeleton key={i} className="h-4 w-full" />
            ))}
          </div>
        </Panel>
      </div>
    )

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl value={range} onChange={setRange} data={ranges()} />
        {/* Reading the numbers is the operator's job; wiping them is not. */}
        {isAdmin && (
          <Button
            size="sm"
            color="red"
            variant="subtle"
            loading={busy}
            onClick={doReset}
          >
            {t('stats.reset')}
          </Button>
        )}
      </div>

      {/* The same area chart the user card draws, for the same reason: two series
          side by side answer "how much came in against how much went out", which a
          single column per day cannot. */}
      <Panel
        title={t('stats.trafficByDay')}
        aside={
          sumDays > 0 && (
            <Mono className="shrink-0 text-xs text-ink-muted">
              ↓ {fmtBytes(sumDown)} · ↑ {fmtBytes(sumUp)}
            </Mono>
          )
        }
        pad
      >
        {series.length === 0 || sumDays === 0 ? (
          <EmptyState
            title={t('stats.noDataForRange')}
            body={t('stats.noDataForRangeHint')}
          />
        ) : (
          <TrafficArea data={chart} height={220} fmt={fmtBytes} />
        )}
      </Panel>

      <Panel title={t('stats.shareByUser')} pad>
        {share.length === 0 ? (
          <EmptyState title={t('stats.noData')} />
        ) : (
          <div className="flex flex-col gap-2">
            {page.shown.map((u) => (
              <ShareBar
                key={u.user_id}
                label={u.name}
                percent={maxShare > 0 ? (u.total / maxShare) * 100 : 0}
                value={fmtBytes(u.total)}
                title={`${u.name} · ↓ ${fmtBytes(u.down)} · ↑ ${fmtBytes(u.up)}`}
              />
            ))}
            <ShowMore rest={page.rest} onClick={page.showMore} className="mt-1" />
          </div>
        )}
      </Panel>

      {/* Only ever rendered on a fleet: with one server the split repeats the total
          above it, and the component says so by rendering nothing. */}
      <NodeTrafficSplit from={from} to={to} title={t('stats.byServer')} />

      {/* Two short reports, side by side where there is room: neither fills a row. */}
      <div className="grid gap-3.5 lg:grid-cols-2">
        <ConnectionCountries />

        <Panel title={t('stats.blocklistMatches')}>
          <AbuseList limit={50} />
        </Panel>
      </div>

      {/* What the security rules have caught. Both read admin-level endpoints, and
          each renders nothing when there is nothing to show. */}
      {isAdmin && (
        <>
          <BlockedList />
          <ProbeList />
        </>
      )}
      {confirmNode}
    </div>
  )
}
