import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { abuseCategoryLabel, getRecentAbuse, getUserAbuse, type AbuseMatch } from './api'
import { useShowMore } from './hooks'
import { currentLang } from './i18n'
import { cn, EmptyState, Mono, ShowMore } from './ui'

// Mirrors model.AbuseRetentionDays. Stated on the panel because it is not the period
// the page's own selector drives — the same reason the connection map states its own,
// which this now matches.
export const ABUSE_WINDOW_DAYS = 30

// AbuseList shows destinations that matched a threat, piracy or gambling blocklist
// — for the whole fleet, or for one user when userId is given.
//
// Unlike TopSites this IS stored, which is the point: it answers "is this account a
// problem" days after the fact, when an abuse complaint arrives. It is also the
// most sensitive thing the panel holds, so it is kept no longer than it is useful for
// (see model.AbuseRetentionDays) and only matches are ever written — ordinary
// browsing never reaches the database.
//
// A match is a signal, not a verdict. Feeds carry false positives, an ad-adjacent
// CDN can land in a threat list, and malware hits usually mean the user's device is
// compromised rather than that the user is misbehaving. The empty state says so.
export function AbuseList({
  userId,
  limit,
  first,
}: {
  userId?: number
  limit?: number
  first?: number
}) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<AbuseMatch[] | null>(null)
  // The poll replaces `rows` every minute, so the reset key is the subject of the
  // list (which user), never the array itself — otherwise an expanded list would
  // snap shut under the operator once a minute.
  const page = useShowMore(rows ?? [], { first, resetKey: userId })

  useEffect(() => {
    let alive = true // guard against an out-of-order response after a prop change
    const load = () =>
      (userId === undefined ? getRecentAbuse(limit) : getUserAbuse(userId, limit))
        .then((d) => alive && setRows(d))
        .catch(() => alive && setRows([]))
    load()
    const t = setInterval(load, 60_000)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [userId, limit])

  if (rows === null) return null // first load: no flash of the empty state

  if (rows.length === 0) {
    return <EmptyState title={t('abuse.noMatches')} />
  }

  // Dense rows, like every other list here: the category as coloured text rather than
  // a badge, the destination in mono, and who/when on the right.
  return (
    <div className="flex flex-col">
      {page.shown.map((r) => {
        const bad = r.category === 'malware' || r.category === 'badip'
        return (
          <div
            key={`${r.user_id}-${r.node_id}-${r.domain}-${r.day}`}
            className="flex items-center justify-between gap-3 border-t border-gray-100 px-3.5 py-[7px] first:border-t-0"
          >
            <div className="flex min-w-0 items-center gap-2">
              <span className={cn('shrink-0 text-xs', bad ? 'text-danger' : 'text-ink-muted')}>
                {abuseCategoryLabel(r.category)}
              </span>
              <Mono className="truncate text-xs text-ink" title={r.domain}>
                {r.domain}
              </Mono>
            </div>
            <Mono className="shrink-0 text-[11px] text-ink-muted">
              {/* Only on the fleet-wide view: inside a user's card the name is the page.
                  The id rides along because names are not unique. */}
              {userId === undefined ? `${r.user_name ? `${r.user_name} ` : ''}#${r.user_id} · ` : ''}
              {r.day} · {r.count.toLocaleString(currentLang())}×
            </Mono>
          </div>
        )
      })}
      <ShowMore rest={page.rest} onClick={page.showMore} className="p-3.5" />
    </div>
  )
}
