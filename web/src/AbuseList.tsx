import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { abuseCategoryLabel, getRecentAbuse, getUserAbuse, type AbuseMatch } from './api'
import { useShowMore } from './hooks'
import { currentLang } from './i18n'
import { Badge, ShowMore } from './ui'

// AbuseList shows destinations that matched a threat, piracy or gambling blocklist
// — for the whole fleet, or for one user when userId is given.
//
// Unlike TopSites this IS stored, which is the point: it answers "is this account a
// problem" days after the fact, when an abuse complaint arrives. It is also the
// most sensitive thing the panel holds, so the window is deliberately short (see
// model.AbuseRetentionDays) and only matches are ever written — ordinary browsing
// never reaches the database.
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
    return (
      <p className="py-2 text-center text-sm text-ink-muted">
        {t('abuse.noMatches')}
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-1.5">
      {page.shown.map((r) => (
        <div
          key={`${r.user_id}-${r.node_id}-${r.domain}-${r.day}`}
          className="flex items-center justify-between gap-2 rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2"
        >
          <div className="flex min-w-0 items-center gap-2">
            <Badge color={r.category === 'malware' || r.category === 'badip' ? 'red' : 'gray'}>
              {abuseCategoryLabel(r.category)}
            </Badge>
            <span className="truncate font-mono text-sm" title={r.domain}>
              {r.domain}
            </span>
          </div>
          <span className="shrink-0 text-xs text-ink-muted">
            {/* Only on the fleet-wide view: inside a user's card the name is the page.
                The id rides along because names are not unique. */}
            {userId === undefined ? `${r.user_name ? `${r.user_name} ` : ''}#${r.user_id} · ` : ''}
            {r.day} · {r.count.toLocaleString(currentLang())}×
          </span>
        </div>
      ))}
      <ShowMore rest={page.rest} onClick={page.showMore} />
    </div>
  )
}
