import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Connection, SubClient } from './api'
import { fmtDuration, fmtLastSeen, fmtStamp, isOnline } from './format'
import { clientName } from './UserUsage'
import {
  Badge,
  cn,
  IconBan,
  IconButton,
  IconUnlock,
  Mono,
  Modal,
  TableShell,
  TD,
  THead,
  TR,
  useCopy,
} from './ui'

// Every field of one address in full — the card's list keeps the row short, and the
// apps behind the address, the count and the estimate it leaves out are here.

// ClientSummary names the apps behind one address on at most two lines: a row that
// grows with every install makes the table unreadable, and the column is a summary — the
// device each app runs on and when it last refreshed are in the hover text.
function ClientSummary({ list }: { list: SubClient[] }) {
  const { t } = useTranslation()
  if (list.length === 0) return <span className="text-xs text-ink-muted">—</span>
  const device = (c: SubClient) => [c.platform, c.model].filter(Boolean).join(', ')
  return (
    <span
      className="line-clamp-2 text-xs text-ink"
      title={list
        .map(
          (c) =>
            `${clientName(c, t('userDetail.unknownApp'))}${device(c) ? ` · ${device(c)}` : ''} — ${t(
              'userDetail.clientRefreshed',
              { time: fmtLastSeen(c.last_seen) },
            )}`,
        )
        .join('\n')}
    >
      {list.map((c) => clientName(c, t('userDetail.unknownApp'))).join(', ')}
    </span>
  )
}

type SortKey = 'ip' | 'count' | 'last_seen'

export function IPDevicesModal({
  open,
  onClose,
  userName,
  conns,
  clientsByIP,
  withoutAddress,
  limit,
  active,
  canBan,
  banBusy,
  onBan,
  onUnban,
}: {
  open: boolean
  onClose: () => void
  userName: string
  conns: Connection[]
  clientsByIP: Map<string, SubClient[]>
  // Installs that refreshed the subscription from an address no connection row names:
  // an old fetch, or one made before the panel kept the address.
  withoutAddress: SubClient[]
  limit: number
  active: number
  canBan: boolean
  banBusy: string | null
  onBan: (ip: string) => void
  onUnban: (ip: string) => void
}) {
  const { t } = useTranslation()
  const [sort, setSort] = useState<SortKey>('last_seen')
  const { copied, copy } = useCopy()

  const rows = useMemo(() => {
    const list = [...conns]
    if (sort === 'ip') return list.sort((a, b) => a.ip.localeCompare(b.ip))
    return list.sort((a, b) => b[sort] - a[sort])
  }, [conns, sort])

  const sortHead = (key: SortKey, label: string) => (
    <button
      type="button"
      className={cn('inline-flex items-center gap-1', sort === key && 'text-ink')}
      onClick={() => setSort(key)}
    >
      {label}
      {sort === key && <span aria-hidden>↓</span>}
    </button>
  )

  const total = conns.filter((c) => !c.tunnel).length

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="2xl"
      title={`${t('userDetail.devices')} · ${userName}`}
      subtitle={
        limit > 0
          ? t('userDetail.activeOfLimit', { active, limit, total })
          : t('userDetail.activeTotal', { active, total })
      }
    >
      <div className="-m-5">
        {rows.length === 0 ? (
          <p className="px-4 py-6 text-center text-xs text-ink-muted">{t('userDetail.noConnections')}</p>
        ) : (
          <TableShell bare>
            <THead
              cols={[
                { label: sortHead('ip', t('userDetail.devIP')), className: 'whitespace-nowrap' },
                { label: t('userDetail.state'), className: 'whitespace-nowrap' },
                { label: sortHead('count', t('userDetail.devConns')), className: 'whitespace-nowrap' },
                { label: t('userDetail.devActive'), className: 'whitespace-nowrap' },
                {
                  label: sortHead('last_seen', t('userDetail.lastConnect')),
                  className: 'whitespace-nowrap',
                },
                { label: t('stats.clients'), className: 'whitespace-nowrap' },
                ...(canBan ? [{ srOnly: t('userDetail.ban') }] : []),
              ]}
            />
            <tbody>
              {rows.map((c) => (
                <TR key={c.ip}>
                  <TD className="align-top">
                    <span className="flex flex-col gap-0.5">
                      <button
                        type="button"
                        className="max-w-44 truncate text-start"
                        title={copied ? t('common.copied') : c.ip}
                        onClick={() => copy(c.ip)}
                      >
                        <Mono className="text-xs">{c.ip}</Mono>
                      </button>
                      {c.tunnel && (
                        <span className="text-[11px] text-ink-muted">
                          {c.tunnel_via?.length ? c.tunnel_via.join(' · ') : t('userDetail.tunnelConn')}
                        </span>
                      )}
                    </span>
                  </TD>
                  <TD className="align-top">
                    {c.banned ? (
                      <Badge color="red" size="xs">
                        {t('userDetail.devBanned')}
                      </Badge>
                    ) : (
                      <span className="flex items-center gap-1.5 whitespace-nowrap text-xs">
                        <span
                          className={cn(
                            'size-1.5 shrink-0 rounded-full',
                            isOnline(c.last_seen) ? 'bg-success' : 'bg-gray-400',
                          )}
                        />
                        {isOnline(c.last_seen) ? t('userDetail.devOnline') : t('userDetail.devOffline')}
                      </span>
                    )}
                  </TD>
                  <TD className="align-top">
                    <Mono className="text-xs">{c.count}</Mono>
                  </TD>
                  <TD className="align-top">
                    <Mono
                      className="text-xs whitespace-nowrap"
                      title={t('userDetail.approxHint', { time: fmtDuration(c.approx_seconds), count: c.count })}
                    >
                      {fmtDuration(c.approx_seconds)}
                    </Mono>
                  </TD>
                  <TD className="align-top">
                    <Mono className="text-xs whitespace-nowrap">{fmtStamp(c.last_seen)}</Mono>
                  </TD>
                  <TD className="align-top">
                    <ClientSummary list={clientsByIP.get(c.ip) ?? []} />
                  </TD>
                  {canBan && (
                    <TD className="text-end">
                      {c.banned ? (
                        <IconButton
                          compact
                          disabled={banBusy !== null}
                          title={t('userDetail.unban')}
                          onClick={() => onUnban(c.ip)}
                        >
                          <IconUnlock size={16} />
                        </IconButton>
                      ) : c.can_ban ? (
                        <IconButton
                          compact
                          color="red"
                          disabled={banBusy !== null}
                          title={t('userDetail.ban')}
                          onClick={() => onBan(c.ip)}
                        >
                          <IconBan size={16} />
                        </IconButton>
                      ) : null}
                    </TD>
                  )}
                </TR>
              ))}
            </tbody>
          </TableShell>
        )}
        {withoutAddress.length > 0 && (
          <>
            <div
              className="flex items-center border-t border-gray-100 bg-gray-50 px-5 py-1.5 text-xs text-ink-muted"
              title={t('userDetail.clientsNoAddressHint')}
              tabIndex={0}
            >
              {t('userDetail.clientsNoAddress')}
            </div>
            {withoutAddress.map((c) => (
              <div
                key={c.id}
                className="flex items-center justify-between gap-3 border-b border-gray-100 px-5 py-2 last:border-0"
              >
                <span className="min-w-0 truncate text-xs">
                  <span className="text-ink">{clientName(c, t('userDetail.unknownApp'))}</span>
                  {[c.platform, c.model].filter(Boolean).length > 0 && (
                    <span className="text-ink-muted">
                      {' · '}
                      {[c.platform, c.model].filter(Boolean).join(', ')}
                    </span>
                  )}
                </span>
                <Mono className="shrink-0 text-[11px] text-ink-muted">
                  {fmtLastSeen(c.last_seen)}
                </Mono>
              </div>
            ))}
          </>
        )}
      </div>
    </Modal>
  )
}
