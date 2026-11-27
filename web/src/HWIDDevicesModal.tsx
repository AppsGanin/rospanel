import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Device } from './api'
import { fmtStamp } from './format'
import {
  cn,
  IconButton,
  IconTrash,
  Modal,
  Mono,
  TableShell,
  TD,
  THead,
  TR,
  useCopy,
} from './ui'

// The bound installs of one user in full: every field the client sent with its
// subscription fetch, which the card's compact list leaves out.

type Platform = 'android' | 'ios' | 'macos' | 'windows' | 'linux' | 'tv' | ''

// platformOf reads the x-device-os value (and, failing that, the User-Agent) as one
// of the platforms the table has a glyph for.
function platformOf(os: string, ua: string): Platform {
  const s = `${os} ${ua}`.toLowerCase()
  if (s.includes('android')) return 'android'
  if (s.includes('tvos') || s.includes('appletv') || s.includes('androidtv')) return 'tv'
  if (/\bios\b|iphone|ipad|ipados/.test(s)) return 'ios'
  if (s.includes('mac')) return 'macos'
  if (s.includes('windows') || s.includes('win64')) return 'windows'
  if (s.includes('linux')) return 'linux'
  return ''
}

const PLATFORM_NAME: Record<Platform, string> = {
  android: 'Android',
  ios: 'iOS',
  macos: 'macOS',
  windows: 'Windows',
  linux: 'Linux',
  tv: 'TV',
  '': '',
}

function PlatformGlyph({ p }: { p: Platform }) {
  const path = (() => {
    switch (p) {
      case 'android':
        return (
          <>
            <rect x="5" y="9" width="14" height="10" rx="2" />
            <path d="M8 9a4 4 0 0 1 8 0M7 4l2 2.5M17 4l-2 2.5M9.5 13h.01M14.5 13h.01" />
          </>
        )
      case 'ios':
        return (
          <>
            <rect x="7" y="2.5" width="10" height="19" rx="2.5" />
            <path d="M11 18.5h2" />
          </>
        )
      case 'macos':
        return (
          <>
            <rect x="3" y="4" width="18" height="12" rx="1.5" />
            <path d="M1.5 19.5h21" />
          </>
        )
      case 'windows':
        return (
          <>
            <rect x="3.5" y="3.5" width="17" height="17" rx="1.5" />
            <path d="M12 3.5v17M3.5 12h17" />
          </>
        )
      case 'linux':
        return <path d="M4 17l6-5-6-5M12 19h8" />
      case 'tv':
        return (
          <>
            <rect x="2.5" y="5" width="19" height="12" rx="1.5" />
            <path d="M8 20.5h8" />
          </>
        )
      default:
        return (
          <>
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <path d="M9 9h6v6H9z" />
          </>
        )
    }
  })()
  return (
    <span className="inline-flex size-7 shrink-0 items-center justify-center rounded-lg border border-brand-600/20 bg-brand-600/8 text-brand-600">
      <svg
        width={16}
        height={16}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        {path}
      </svg>
    </span>
  )
}

type SortKey = 'first_seen' | 'last_seen'

export function HWIDDevicesModal({
  open,
  onClose,
  userName,
  devices,
  limit,
  canManage,
  onUnbind,
}: {
  open: boolean
  onClose: () => void
  userName: string
  devices: Device[]
  limit: number
  canManage: boolean
  // No hwid: every device.
  onUnbind: (hwid?: string) => void
}) {
  const { t } = useTranslation()
  const [sort, setSort] = useState<SortKey>('last_seen')
  const { copied, copy } = useCopy()
  const [copiedHwid, setCopiedHwid] = useState('')

  const rows = useMemo(
    () => [...devices].sort((a, b) => b[sort] - a[sort]),
    [devices, sort],
  )

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

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="2xl"
      title={`${t('userDetail.boundDevices')} · ${userName}`}
      subtitle={
        limit > 0
          ? t('userDetail.boundOfLimit', { count: devices.length, limit })
          : t('userDetail.boundTotal', { count: devices.length })
      }

    >
      <div className="-m-5">
        {rows.length === 0 ? (
          <p className="px-4 py-6 text-center text-xs text-ink-muted">
            {t('userDetail.noBoundDevices')}
          </p>
        ) : (
          <TableShell bare>
            <THead
              cols={[
                { label: t('userDetail.devPlatform'), className: 'whitespace-nowrap' },
                { label: 'HWID', className: 'whitespace-nowrap' },
                { label: t('userDetail.devIP'), className: 'whitespace-nowrap' },
                { label: t('userDetail.devModel'), className: 'whitespace-nowrap' },
                { label: 'User-Agent', className: 'hidden whitespace-nowrap lg:table-cell' },
                { label: sortHead('first_seen', t('userDetail.devAdded')), className: 'whitespace-nowrap' },
                { label: sortHead('last_seen', t('userDetail.devUpdated')), className: 'whitespace-nowrap' },
                ...(canManage ? [{ srOnly: t('userDetail.unbind') }] : []),
              ]}
            />
            <tbody>
              {rows.map((d) => {
                const p = platformOf(d.os, d.app)
                const os = [PLATFORM_NAME[p] || d.os, d.os_version].filter(Boolean).join(' ')
                return (
                  <TR key={d.hwid}>
                    <TD>
                      <span className="flex items-center gap-2 whitespace-nowrap">
                        <PlatformGlyph p={p} />
                        <span className="text-xs text-ink">{os || '—'}</span>
                      </span>
                    </TD>
                    <TD>
                      <button
                        type="button"
                        className="max-w-40 truncate text-start"
                        title={copied && copiedHwid === d.hwid ? t('common.copied') : d.hwid}
                        onClick={() => {
                          setCopiedHwid(d.hwid)
                          copy(d.hwid)
                        }}
                      >
                        <Mono className="text-xs">{d.hwid}</Mono>
                      </button>
                    </TD>
                    <TD>
                      <Mono className="text-xs whitespace-nowrap">{d.ip || '—'}</Mono>
                    </TD>
                    <TD>
                      <span className="block max-w-44 truncate text-xs" title={d.model}>
                        {d.model || '—'}
                      </span>
                    </TD>
                    <TD className="hidden lg:table-cell">
                      <Mono className="block max-w-52 truncate text-xs" title={d.app}>
                        {d.app || '—'}
                      </Mono>
                    </TD>
                    <TD>
                      <Mono className="text-xs whitespace-nowrap">{fmtStamp(d.first_seen)}</Mono>
                    </TD>
                    <TD>
                      <Mono className="text-xs whitespace-nowrap">{fmtStamp(d.last_seen)}</Mono>
                    </TD>
                    {canManage && (
                      <TD className="text-end">
                        <IconButton
                          color="red"
                          title={t('userDetail.unbind')}
                          onClick={() => onUnbind(d.hwid)}
                        >
                          <IconTrash size={16} />
                        </IconButton>
                      </TD>
                    )}
                  </TR>
                )
              })}
            </tbody>
          </TableShell>
        )}
      </div>
    </Modal>
  )
}
