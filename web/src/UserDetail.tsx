import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { QRCodeSVG } from 'qrcode.react'
import {
  bulkUsers,
  MAX_DEVICE_LIMIT,
  deleteUser,
  genUserTelegramLink,
  getBilling,
  getStatsSeries,
  getUserConnections,
  getUserDevices,
  unbindUserDevice,
  renameUser,
  resetUserTraffic,
  rotateSubToken,
  messageUser,
  unlinkUserTelegram,
  setResetPeriod,
  setUserEnabled,
  setUserLimits,
  setUserPlan,
  setUserGroups,
  setUserNote,
  setUserTags,
  listGroups,
  listUserTags,
  type Connection,
  type DailyPoint,
  type DeviceList,
  type Group,
  type TagCount,
  type TariffPlan,
  type User,
} from './api'
import {
  dateToUnixEndOfDay,
  deviceLimitOptions,
  fmtBytes,
  fmtExpire,
  fmtLastSeen,
  fmtQuota,
  fmtSpeed,
  gbToBytes,
  isOnline,
  localDay,
  quotaOptions,
  ranges,
  resetPeriods,
  speedLimitOptions,
  statusInfo,
  unixToLocalDate,
} from './format'
import { useAction, useShowMore } from './hooks'
import { HtmlEditor } from './HtmlEditor'
import { errMessage, notifyError, notifySuccess } from './notify'
import { TrafficArea } from './charts'
import { NodeTrafficSplit } from './NodeTrafficSplit'
import { AbuseList } from './AbuseList'
import { UserEventsModal } from './UserEventsModal'
import {
  Button,
  cn,
  CustomizableSelect,
  Code,
  DatePicker,
  Drawer,
  Modal,
  Mono,
  IconCheck,
  IconClose,
  IconCopy,
  Panel,
  SegmentedControl,
  Select,
  ShowMore,
  Switch,
  TagsInput,
  Textarea,
  TextInput,
  useConfirm,
  useCopy,
} from './ui'
import i18n, { currentLang } from './i18n'

// planSelectData builds the tariff dropdown: "manual" plus enabled plans, and a
// fallback entry if the user is on a plan that's hidden/disabled (so the current
// value still resolves to a label).
function planSelectData(plans: TariffPlan[], user: User) {
  const data = [
    { value: '0', label: i18n.t('userDetail.manual') },
    ...plans
      .filter((p) => p.enabled)
      .map((p) => ({
        value: String(p.id),
        label: p.name,
      })),
  ]
  if (user.plan_id && !data.some((o) => o.value === String(user.plan_id))) {
    data.push({
      value: String(user.plan_id),
      label: user.plan_name || i18n.t('userDetail.planNum', { id: user.plan_id }),
    })
  }
  return data
}



// optLabel resolves a select value to its human label, for the confirmation text.
function optLabel(data: { value: string; label: string }[], value: string): string {
  return data.find((o) => o.value === value)?.label ?? value
}

// fmtLimitOption names a limit value: the preset's label when it is one, the
// formatter's wording otherwise (a value typed by hand).
function fmtLimitOption(
  data: { value: string; label: string }[],
  value: string,
  format: (n: number) => string,
): string {
  return data.find((o) => o.value === value)?.label ?? format(Number(value))
}


// resetLabel renders a reset period for display. Beyond the fixed resetPeriods()
// options it also handles the "days:N" rolling cycle that a free plan writes
// (see planLimits in internal/core/manager_billing.go), which has no entry there.
function resetLabel(v: string): string {
  const m = /^days:(\d+)$/.exec(v)
  if (m) return i18n.t('userDetail.everyNDays', { count: Number(m[1]) })
  return optLabel(resetPeriods(), v || 'none')
}

// dateLabel renders an expiry (unix or a "YYYY-MM-DD" picker value) for the
// confirmation text.
function dateLabel(v: number | string): string {
  if (!v) return i18n.t('common.never')
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v)
  return d.toLocaleDateString(currentLang())
}

// StateRow is one fact about the account: what it is on the left, muted; what it
// says on the right, mono when it is a number. Rows divide; they are not boxed.
function StateRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0">
      <span className="shrink-0 text-xs text-ink-muted">{label}</span>
      <span className="min-w-0 truncate text-right text-xs text-ink">{children}</span>
    </div>
  )
}

// RenameModal is the account's name, changed on purpose. It was an inline pencil in
// the drawer header; at 520px that header holds the name, the id and the close
// button, and an input had nowhere to grow.
function RenameModal({
  user,
  open,
  onClose,
  onChanged,
}: {
  user: User
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(user.name)
  const { busy, run } = useAction()

  useEffect(() => {
    if (open) setDraft(user.name)
  }, [open, user.name])

  const save = () => {
    const name = draft.trim()
    if (!name || name === user.name) return onClose()
    run(async () => {
      await renameUser(user.id, name)
      onChanged()
      onClose()
    })
  }

  return (
    <Modal open={open} onClose={onClose} title={t('userDetail.rename')}>
      <div className="flex flex-col gap-4">
        <TextInput label={t('usersPanel.name')} value={draft} onChange={setDraft} autoFocus />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button loading={busy} disabled={!draft.trim()} onClick={save}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ExtendUserModal adds days to one account's expiry through the same server path the
// list's bulk action uses — one user and fifty must not behave differently.
function ExtendUserModal({
  user,
  open,
  onClose,
  onChanged,
}: {
  user: User
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [days, setDays] = useState('30')
  const { busy, run } = useAction()
  const n = Math.floor(Number(days) || 0)

  return (
    <Modal open={open} onClose={onClose} title={t('usersPanel.extendTitle')}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-ink-muted">
          {t('userDetail.extendUserBody', { name: user.name, date: fmtExpire(user.expire_at) })}
        </p>
        <div className="flex flex-wrap gap-2">
          {EXTEND_PRESETS.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={n === p ? 'filled' : 'light'}
              color="gray"
              onClick={() => setDays(String(p))}
            >
              {t('usersPanel.plusDays', { count: p })}
            </Button>
          ))}
        </div>
        <TextInput label={t('usersPanel.days')} type="number" value={days} onChange={setDays} />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            loading={busy}
            disabled={n <= 0}
            onClick={() =>
              run(async () => {
                await bulkUsers([user.id], 'extend', n)
                onChanged()
                notifySuccess(t('userDetail.extended', { count: n }))
                onClose()
              })
            }
          >
            {t('usersPanel.extendByDays', { count: n })}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// EXTEND_PRESETS mirrors the list's bulk dialog, so the same four choices are offered
// wherever a subscription is extended.
const EXTEND_PRESETS = [7, 30, 90, 180]

export function UserDetail({
  user,
  onClose,
  onChanged,
  userBotEnabled,
}: {
  user: User | null
  onClose: () => void
  onChanged: () => void
  userBotEnabled: boolean
}) {
  const { t } = useTranslation()
  const [series, setSeries] = useState<DailyPoint[]>([])
  const [conns, setConns] = useState<Connection[]>([])
  // Bound installs (HWID). Null until the first load, and left empty when the
  // operator hasn't switched device binding on — the whole block then stays hidden.
  const [bound, setBound] = useState<DeviceList | null>(null)
  const [range, setRange] = useState('30')
  const [billingOn, setBillingOn] = useState(false)
  const [plans, setPlans] = useState<TariffPlan[]>([])
  const [tgLink, setTgLink] = useState<{ url: string; mins: number } | null>(null)
  const [eventsOpen, setEventsOpen] = useState(false)
  const [msgOpen, setMsgOpen] = useState(false)
  const [msgText, setMsgText] = useState('')
  const [msgMedia, setMsgMedia] = useState<File | null>(null)
  const msgFileRef = useRef<HTMLInputElement>(null)
  const [sending, setSending] = useState(false)
  const [allGroups, setAllGroups] = useState<Group[]>([])
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [groupQuery, setGroupQuery] = useState('')
  const [savingGroups, setSavingGroups] = useState(false)
  // The limits section is a draft: a tariff and four caps are one decision, and
  // applying each keystroke would reconcile Xray five times for one edit.
  const [dPlan, setDPlan] = useState('0')
  const [dExpire, setDExpire] = useState('')
  const [dLimitGb, setDLimitGb] = useState('0')
  const [dDeviceLimit, setDDeviceLimit] = useState('0')
  const [dSpeedLimit, setDSpeedLimit] = useState('0')
  const [dReset, setDReset] = useState('none')
  const [savingLimits, setSavingLimits] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [extendOpen, setExtendOpen] = useState(false)
  const email = useCopy()
  const { confirm, confirmNode } = useConfirm()

  useEffect(() => {
    setTgLink(null) // a one-time bind link is per-user; don't leak it across switches
    setEventsOpen(false) // ditto for the journal — never show one user's trail over another
    setRenaming(false)
    setExtendOpen(false)
    setSel(new Set((user?.groups ?? []).map((g) => g.id)))
    setGroupQuery('')
    resetLimitDraft()
    // resetLimitDraft is defined below and closes over `user`; re-running it on every
    // user change is the whole point.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user])

  // All groups, for the access-group selector. Loaded once the card opens.
  useEffect(() => {
    if (!user) return
    let alive = true
    listGroups()
      .then((g) => alive && setAllGroups(g))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user])

  useEffect(() => {
    if (!user) {
      setSeries([])
      return
    }
    let alive = true // guard against an out-of-order response after a user switch
    const from = localDay(Number(range) - 1)
    getStatsSeries({ user_id: user.id, from, to: localDay(0) })
      .then((d) => alive && setSeries(d))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user, range])

  useEffect(() => {
    if (!user) {
      setConns([])
      return
    }
    let alive = true
    const load = () =>
      getUserConnections(user.id)
        .then((d) => alive && setConns(d))
        .catch(() => {})
    load()
    const t = setInterval(load, 30_000)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [user])

  useEffect(() => {
    if (!user) {
      setBound(null)
      return
    }
    let alive = true
    const load = () =>
      getUserDevices(user.id)
        .then((d) => alive && setBound(d))
        .catch(() => {})
    load()
    // Same cadence as the connection list: a device appears when its app refreshes
    // the subscription, which is minutes apart, not seconds.
    const t = setInterval(load, 30_000)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [user])

  // Tariffs (only meaningful when billing is enabled); loaded once the card opens.
  useEffect(() => {
    if (!user) return
    let alive = true
    getBilling()
      .then((b) => {
        if (!alive) return
        setBillingOn(!!b.enabled)
        setPlans(b.plans ?? [])
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user])

  const chart = series.map((p) => ({ day: p.day.slice(5), up: p.up, down: p.down }))
  const fail = (e: unknown) => notifyError(errMessage(e))

  // A cap set through the API may not be one of the presets; keep it in the list so
  // the select shows what the user actually has instead of falling back to the first
  // option (which would read as "unlimited").
  const speedData = speedLimitOptions().some((o) => o.value === dSpeedLimit)
    ? speedLimitOptions()
    : [...speedLimitOptions(), { value: dSpeedLimit, label: fmtSpeed(Number(dSpeedLimit)) }]

  const quotaData = user
    ? quotaOptions().some((o) => o.value === dLimitGb)
      ? quotaOptions()
      : [...quotaOptions(), { value: dLimitGb, label: fmtBytes(user.data_limit) }]
    : quotaOptions()

  // resetLimitDraft snaps the draft back to what the server says the user is.
  function resetLimitDraft() {
    setDPlan(String(user?.plan_id || 0))
    setDExpire(unixToLocalDate(user?.expire_at ?? 0))
    setDLimitGb(user && user.data_limit ? String(user.data_limit / (1024 * 1024 * 1024)) : '0')
    setDDeviceLimit(String(user?.device_limit ?? 0))
    setDSpeedLimit(String(user?.speed_limit ?? 0))
    setDReset(user?.reset_period || 'none')
  }

  const planManaged = billingOn && dPlan !== '0'

  const limitsDirty =
    user != null &&
    (dPlan !== String(user.plan_id || 0) ||
      (!planManaged &&
        (dExpire !== unixToLocalDate(user.expire_at) ||
          gbToBytes(Number(dLimitGb)) !== user.data_limit ||
          Number(dDeviceLimit) !== (user.device_limit ?? 0) ||
          Number(dSpeedLimit) !== (user.speed_limit ?? 0) ||
          dReset !== (user.reset_period || 'none'))))

  // Order matters: applying a tariff overwrites the quota, the device cap and the
  // reset cycle (planWriteFor, core/manager_billing.go), so the plan goes first and
  // hand-set limits only follow when the account is on "manual".
  const saveLimitDraft = async () => {
    if (!user) return
    setSavingLimits(true)
    try {
      if (dPlan !== String(user.plan_id || 0)) await setUserPlan(user.id, Number(dPlan))
      if (dPlan === '0') {
        await setUserLimits(
          user.id,
          gbToBytes(Number(dLimitGb)),
          dateToUnixEndOfDay(dExpire),
          Number(dDeviceLimit),
          Number(dSpeedLimit),
        )
        if (dReset !== (user.reset_period || 'none')) await setResetPeriod(user.id, dReset)
      }
      onChanged()
      notifySuccess(t('common.saved'))
    } catch (e) {
      fail(e)
    } finally {
      setSavingLimits(false)
    }
  }

  // Group membership is applied on a button, not per chip: each save reconciles Xray,
  // so toggling several groups at once should be one restart, not several.
  const current = new Set((user?.groups ?? []).map((g) => g.id))
  const groupsDirty =
    user != null && (sel.size !== current.size || [...sel].some((id) => !current.has(id)))
  const toggleGroup = (id: number, on: boolean) =>
    setSel((prev) => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })
  const resetGroups = () => setSel(new Set(current))
  const applyGroups = () => {
    if (!user) return
    setSavingGroups(true)
    setUserGroups(user.id, [...sel])
      .then(onChanged)
      .then(() => notifySuccess(t('userDetail.groupsUpdated')))
      .catch(fail)
      .finally(() => setSavingGroups(false))
  }
  // A user whose ONLY selected groups grant nothing sees no connections at all — a
  // silent lockout that's easy to create by accident (an empty group revokes rather
  // than grants). Warn before it's applied.
  const selectedGrantCount = allGroups
    .filter((g) => sel.has(g.id))
    .reduce((n, g) => n + (g.grants?.length ?? 0), 0)
  const groupQ = groupQuery.trim().toLowerCase()
  const selectedGroups = allGroups.filter((g) => sel.has(g.id))
  const availableGroups = allGroups.filter(
    (g) => !sel.has(g.id) && (!groupQ || g.name.toLowerCase().includes(groupQ)),
  )
  const showGroupSearch = allGroups.length > 8

  // Unbinding frees a slot immediately — the device can rebind on its next fetch, so
  // this is "let them re-add it", not a ban. Confirmed all the same: for the owner it
  // means their app stops updating until it refetches.
  const unbindDevice = async (hwid?: string) => {
    if (!user) return
    const ok = await confirm({
      title: t(hwid ? 'userDetail.unbindTitle' : 'userDetail.unbindAllTitle'),
      body: t(hwid ? 'userDetail.unbindBody' : 'userDetail.unbindAllBody', { name: user.name }),
      confirmLabel: t('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await unbindUserDevice(user.id, hwid ? { hwid } : { all: true })
      setBound(await getUserDevices(user.id))
    } catch (e) {
      fail(e)
    }
  }

  const activeConnCount = user ? conns.filter((c) => isOnline(c.last_seen)).length : 0
  // Devices are the longest list in the card (the server hands over up to 20 IPs) and
  // sit between two sections the operator scrolls to, so only the most recent few are
  // open by default. Keyed on the user so reopening the card for someone else starts
  // collapsed again.
  const devices = useShowMore(conns, { first: 5, resetKey: user?.id })

  return (
    <>
    <Drawer
      open={!!user}
      onClose={onClose}
      side="right"
      title={
        user ? (
          <span className="flex min-w-0 items-center gap-2">
            <span
              className={cn(
                'size-2 shrink-0 rounded-full',
                isOnline(user.last_seen) ? 'bg-success' : 'bg-gray-400',
              )}
            />
            <span className="truncate text-[15px] font-bold text-ink">{user.name}</span>
            <Mono className="shrink-0 text-[11px] font-normal text-ink-muted">
              {user.system_email}
            </Mono>
            <button
              onClick={() => email.copy(user.system_email)}
              className="shrink-0 text-gray-400 transition hover:text-accent"
              title={t('common.copy')}
            >
              {email.copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
            </button>
          </span>
        ) : undefined
      }
    >
      {user && (
        <div className="flex flex-col gap-3.5">
          {/* 1. Banners, each only for its own state. */}
          {user.status === 'device_limited' && (
            <p className="warning-tint rounded-lg px-3 py-2 text-xs text-warning">
              {t('userDetail.bannerDeviceLimit', {
                active: user.active_devices,
                limit: user.device_limit,
              })}
            </p>
          )}
          {user.status === 'expired' && (
            <p className="danger-tint rounded-lg px-3 py-2 text-xs text-danger">
              {t('userDetail.bannerExpired', { date: fmtExpire(user.expire_at) })}
            </p>
          )}
          {user.abuse_action && user.abuse_until && (
            <p className="warning-tint rounded-lg px-3 py-2 text-xs text-warning">
              {t(
                user.abuse_action === 'disable'
                  ? 'userDetail.abuseDisabled'
                  : 'userDetail.abuseThrottled',
                { when: new Date(user.abuse_until * 1000).toLocaleString(i18n.language) },
              )}
            </p>
          )}

          {/* 2. The three things done to an account most often. */}
          <div className="flex flex-wrap gap-2">
            <Button size="sm" href={user.sub_url} target="_blank">
              {t('userDetail.subLink')}
            </Button>
            <Button size="sm" variant="outline" color="gray" onClick={() => setExtendOpen(true)}>
              {t('usersPanel.extend')}
            </Button>
            <Button
              size="sm"
              variant="outline"
              color="gray"
              onClick={async () => {
                const ok = await confirm({
                  title: t('userDetail.resetTrafficTitle'),
                  body: t('userDetail.resetTrafficBody', { name: user.name }),
                  confirmLabel: t('usersPanel.reset'),
                  danger: true,
                })
                if (ok) resetUserTraffic(user.id).then(onChanged).catch(fail)
              }}
            >
              {t('usersPanel.resetTraffic')}
            </Button>
          </div>

          {/* 3. State: what the account is right now. The switch applies at once —
                 it is a switch; everything else here is read-only. */}
          <Panel title={t('userDetail.state')}>
            <StateRow label={t('usersPanel.subscription')}>
              <Switch
                checked={user.enabled}
                onChange={(v) => setUserEnabled(user.id, v).then(onChanged).catch(fail)}
              />
            </StateRow>
            <StateRow label={t('usersPanel.colTraffic')}>
              <Mono>{fmtQuota(user.used_up + user.used_down, user.data_limit)}</Mono>
            </StateRow>
            <StateRow label={t('usersPanel.colExpires')}>
              <Mono>{fmtExpire(user.expire_at)}</Mono>
            </StateRow>
            <StateRow label={t('userDetail.devices')}>
              <Mono className={user.status === 'device_limited' ? 'text-warning' : undefined}>
                {user.device_limit > 0
                  ? `${user.active_devices}/${user.device_limit}`
                  : String(activeConnCount)}
              </Mono>
            </StateRow>
            <StateRow label={t('userDetail.lastOnline')}>
              <Mono>{fmtLastSeen(user.last_seen)}</Mono>
            </StateRow>
            <StateRow label={t('groups.title')}>
              {(user.groups ?? []).length === 0 ? (
                <span className="text-ink-muted">{t('userDetail.allConnections')}</span>
              ) : (
                <span className="text-accent">
                  {(user.groups ?? []).map((g) => g.name).join(', ')}
                </span>
              )}
            </StateRow>
          </Panel>

          {/* 4. Devices: bound installs when HWID is on, otherwise the addresses the
                 subscription has been fetched from. */}
          <Panel
            title={t('userDetail.devices')}
            aside={
              <span className="text-xs text-ink-muted">
                {user.device_limit > 0
                  ? t('userDetail.activeOfLimit', {
                      active: activeConnCount,
                      limit: user.device_limit,
                      total: conns.length,
                    })
                  : t('userDetail.activeTotal', {
                      active: activeConnCount,
                      total: conns.length,
                    })}
              </span>
            }
          >
            {bound?.enabled ? (
              bound.devices.length === 0 ? (
                <p className="px-3.5 py-3 text-xs text-ink-muted">
                  {t('userDetail.noBoundDevices')}
                </p>
              ) : (
                bound.devices.map((d) => (
                  <div
                    key={d.hwid}
                    className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0"
                  >
                    <span className="flex min-w-0 flex-col">
                      <Mono className="truncate text-xs text-ink">{d.ip || d.hwid}</Mono>
                      <span className="truncate text-xs text-ink-muted">
                        {[d.model, d.os, d.os_version].filter(Boolean).join(' · ') || d.hwid}
                      </span>
                    </span>
                    <span className="flex shrink-0 items-center gap-3">
                      <Mono className="text-[11px] text-ink-muted">{fmtLastSeen(d.last_seen)}</Mono>
                      <button
                        onClick={() => unbindDevice(d.hwid)}
                        className="text-xs text-danger hover:underline"
                      >
                        {t('userDetail.unbind')}
                      </button>
                    </span>
                  </div>
                ))
              )
            ) : conns.length === 0 ? (
              <p className="px-3.5 py-3 text-xs text-ink-muted">
                {t('userDetail.noConnections')}
              </p>
            ) : (
              <>
                {devices.shown.map((c) => (
                  <div
                    key={c.ip}
                    className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0"
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <span
                        className={cn(
                          'size-1.5 shrink-0 rounded-full',
                          isOnline(c.last_seen) ? 'bg-success' : 'bg-gray-400',
                        )}
                      />
                      <Mono className="truncate text-xs text-ink">{c.ip}</Mono>
                    </span>
                    <Mono className="shrink-0 text-[11px] text-ink-muted">
                      {fmtLastSeen(c.last_seen)} · {c.count}×
                    </Mono>
                  </div>
                ))}
                {devices.rest > 0 && (
                  <div className="px-3.5 py-2">
                    <ShowMore rest={devices.rest} onClick={devices.showMore} />
                  </div>
                )}
              </>
            )}
            {bound?.enabled && bound.devices.length > 0 && (
              <div className="border-t border-gray-100 px-3.5 py-2">
                <Button variant="subtle" color="red" size="xs" onClick={() => unbindDevice()}>
                  {t('userDetail.unbindAll')}
                </Button>
              </div>
            )}
          </Panel>

          {/* 5. Tariff and limits. A tariff owns the quota, the device cap and the
                 reset cycle, so under one the fields are shown disabled rather than
                 hidden: the operator sees what the plan set, and why they cannot
                 edit it. Fields are a draft until Save. */}
          <Panel title={t('userDetail.planAndLimits')} pad bodyClassName="flex flex-col gap-3">
            {billingOn && (
              <>
                <Select
                  label={t('userDetail.plan')}
                  data={planSelectData(plans, user)}
                  value={dPlan}
                  onChange={setDPlan}
                />
                {planManaged && (
                  <p className="-mt-1.5 text-xs text-ink-muted">{t('userDetail.planHint')}</p>
                )}
              </>
            )}
            <DatePicker
              label={t('usersPanel.validUntil')}
              value={dExpire}
              onChange={setDExpire}
              disabled={planManaged}
            />
            <Select
              label={t('usersPanel.trafficLimit')}
              data={quotaData}
              value={dLimitGb}
              onChange={setDLimitGb}
              disabled={planManaged}
            />
            <CustomizableSelect
              label={t('userDetail.deviceLimit')}
              data={deviceLimitOptions()}
              value={dDeviceLimit}
              format={(n) => t('devices.count', { count: n })}
              max={MAX_DEVICE_LIMIT}
              onChange={setDDeviceLimit}
              disabled={planManaged}
            />
            <p className="-mt-1.5 text-xs text-ink-muted">{t('userDetail.deviceLimitHint')}</p>
            <CustomizableSelect
              label={t('userDetail.speedLimit')}
              data={speedData}
              value={dSpeedLimit}
              format={fmtSpeed}
              units={[
                { factor: 1, label: t('speed.unitKbit') },
                { factor: 1000, label: t('speed.unitMbit') },
              ]}
              onChange={setDSpeedLimit}
              disabled={planManaged}
            />
            <p className="-mt-1.5 text-xs text-ink-muted">{t('userDetail.speedLimitHint')}</p>
            <Select
              label={t('usersPanel.autoReset')}
              data={resetPeriods()}
              value={dReset}
              onChange={setDReset}
              disabled={planManaged}
            />
            {limitsDirty && (
              <div className="flex justify-end gap-2 border-t border-gray-100 pt-3">
                <Button size="sm" variant="light" color="gray" onClick={resetLimitDraft} disabled={savingLimits}>
                  {t('common.cancel')}
                </Button>
                <Button size="sm" loading={savingLimits} onClick={saveLimitDraft}>
                  {t('common.save')}
                </Button>
              </div>
            )}
          </Panel>

          {/* 6. The operator's own annotation of the account. */}
          <Panel title={t('userDetail.noteAndTags')} pad>
            <NoteAndTags user={user} onChanged={onChanged} />
          </Panel>

          {/* Access groups: which connections this account may use. Applied on a
              button because each save reconciles Xray — several toggles should be
              one restart, not several. */}
          {allGroups.length > 0 && (
            <Panel
              title={t('groups.title')}
              pad
              bodyClassName="flex flex-col gap-2.5"
              aside={
                <span className="text-xs text-ink-muted">
                  {sel.size === 0
                    ? t('userDetail.allConnections')
                    : t('groups.nSelected', { count: sel.size })}
                </span>
              }
            >
              {selectedGroups.length > 0 ? (
                <div className="flex flex-wrap gap-1.5">
                  {selectedGroups.map((g) => (
                    <GroupChip
                      key={g.id}
                      name={g.name}
                      count={g.grants?.length ?? 0}
                      state="on"
                      onClick={() => toggleGroup(g.id, false)}
                    />
                  ))}
                </div>
              ) : (
                <p className="text-xs text-ink-muted">{t('userDetail.noGroups')}</p>
              )}

              {(availableGroups.length > 0 || groupQ) && (
                <div className="flex flex-col gap-1.5 border-t border-gray-100 pt-2.5">
                  <span className="text-xs text-ink-muted">{t('userDetail.addToGroup')}</span>
                  {showGroupSearch && (
                    <TextInput
                      value={groupQuery}
                      onChange={setGroupQuery}
                      placeholder={t('userDetail.searchGroup')}
                    />
                  )}
                  {availableGroups.length > 0 ? (
                    <div className="flex flex-wrap gap-1.5">
                      {availableGroups.map((g) => (
                        <GroupChip
                          key={g.id}
                          name={g.name}
                          count={g.grants?.length ?? 0}
                          state="add"
                          onClick={() => toggleGroup(g.id, true)}
                        />
                      ))}
                    </div>
                  ) : (
                    <p className="px-0.5 text-xs text-ink-muted">{t('common.nothingFound')}</p>
                  )}
                </div>
              )}

              {sel.size > 0 && selectedGrantCount === 0 && (
                <p className="warning-tint rounded-lg px-2.5 py-1.5 text-xs text-warning">
                  {t('userDetail.groupsGrantNothing')}
                </p>
              )}

              {groupsDirty && (
                <div className="flex justify-end gap-2 border-t border-gray-100 pt-2.5">
                  <Button size="sm" variant="light" color="gray" onClick={resetGroups} disabled={savingGroups}>
                    {t('common.cancel')}
                  </Button>
                  <Button size="sm" loading={savingGroups} onClick={applyGroups}>
                    {t('common.apply')}
                  </Button>
                </div>
              )}
            </Panel>
          )}

          {/* The subscription itself: the QR an operator hands over, and the link. */}
          <Panel title={t('usersPanel.subscription')} pad bodyClassName="flex flex-col gap-3">
            <div className="flex justify-center">
              <div className="rounded-lg bg-onaccent p-3">
                <QRCodeSVG value={user.sub_url} size={168} />
              </div>
            </div>
            <Code block copy>{user.sub_url}</Code>
          </Panel>

          <Panel title="Telegram" pad bodyClassName="flex flex-col gap-2">
            {user.telegram_linked ? (
              <>
                <p className="text-xs text-success">{t('userDetail.botLinked')}</p>
                {!!user.tg_chat_id && (
                  <p className="text-xs text-ink-muted">
                    Telegram ID: <Code copy>{String(user.tg_chat_id)}</Code>
                  </p>
                )}
                <div className="flex flex-wrap gap-2">
                  {/* A broadcast to one person. Shown only with a linked chat AND a
                      running user bot — it is the bot that delivers, so without it the
                      button could only ever produce an error. */}
                  {userBotEnabled && (
                    <Button size="xs" variant="outline" color="gray" onClick={() => setMsgOpen(true)}>
                      {t('userDetail.sendMessage')}
                    </Button>
                  )}
                  <Button
                    size="xs"
                    variant="outline"
                    color="gray"
                    onClick={async () => {
                      const ok = await confirm({
                        title: t('userDetail.unlinkTitle'),
                        body: t('userDetail.unlinkBody'),
                        confirmLabel: t('userDetail.unlink'),
                        danger: true,
                      })
                      if (ok) unlinkUserTelegram(user.id).then(onChanged).catch(fail)
                    }}
                  >
                    {t('userDetail.unlinkTelegram')}
                  </Button>
                </div>
              </>
            ) : user.telegram_link ? (
              <>
                <p className="text-xs text-ink-muted">{t('userDetail.linkHint')}</p>
                <Button
                  size="xs"
                  variant="outline"
                  color="gray"
                  className="self-start"
                  onClick={() =>
                    genUserTelegramLink(user.id)
                      .then((r) =>
                        setTgLink({ url: r.deep_link, mins: Math.round(r.expires_sec / 60) }),
                      )
                      .catch(fail)
                  }
                >
                  {t('userDetail.getLink')}
                </Button>
                {tgLink && (
                  <>
                    <Code block copy>{tgLink.url}</Code>
                    <p className="text-xs text-ink-muted">
                      {t('userDetail.linkNote', { mins: tgLink.mins })}
                    </p>
                  </>
                )}
              </>
            ) : (
              <p className="text-xs text-ink-muted">{t('userDetail.enableUserBot')}</p>
            )}
          </Panel>

          <Panel title={t('stats.blocklistMatches')}>
            <AbuseList userId={user.id} first={5} />
          </Panel>

          <Panel title={t('userDetail.traffic')} pad bodyClassName="flex flex-col gap-3">
            <SegmentedControl fullWidth value={range} onChange={setRange} data={ranges()} />
            {chart.length === 0 ? (
              <p className="py-3 text-center text-xs text-ink-muted">{t('stats.noData')}</p>
            ) : (
              <>
                <TrafficArea data={chart} height={180} fmt={fmtBytes} />
                <NodeTrafficSplit
                  userId={user.id}
                  from={localDay(Number(range) - 1)}
                  to={localDay(0)}
                />
              </>
            )}
          </Panel>

          {/* 7. What is done to the account itself, rather than to its settings. */}
          <div className="flex flex-wrap gap-2 pb-2">
            <Button size="sm" variant="outline" color="gray" onClick={() => setRenaming(true)}>
              {t('userDetail.rename')}
            </Button>
            <Button
              size="sm"
              variant="outline"
              color="gray"
              onClick={async () => {
                const ok = await confirm({
                  title: t('userDetail.rotateTitle'),
                  body: t('userDetail.rotateBody'),
                  confirmLabel: t('userDetail.rotateConfirm'),
                  danger: true,
                })
                if (!ok) return
                rotateSubToken(user.id)
                  .then(() => {
                    notifySuccess(t('userDetail.rotated'))
                    onChanged()
                  })
                  .catch(fail)
              }}
            >
              {t('userDetail.rotate')}
            </Button>
            <Button size="sm" variant="outline" color="gray" onClick={() => setEventsOpen(true)}>
              {t('events.title')}
            </Button>
            <Button
              size="sm"
              variant="light"
              color="red"
              onClick={async () => {
                const ok = await confirm({
                  title: t('userDetail.deleteTitle'),
                  body: t('userDetail.deleteBody', { name: user.name }),
                  confirmLabel: t('common.delete'),
                  danger: true,
                })
                if (ok) {
                  deleteUser(user.id)
                    .then(() => {
                      onChanged()
                      onClose()
                    })
                    .catch(fail)
                }
              }}
            >
              {t('userDetail.deleteUser')}
            </Button>
          </div>
        </div>
      )}
    </Drawer>

    {/* Renaming happens in a dialog rather than in the drawer header: the header is
        520px shared with the id and the close button, and an input there had nowhere
        to grow. */}
    {user && (
      <RenameModal
        user={user}
        open={renaming}
        onClose={() => setRenaming(false)}
        onChanged={onChanged}
      />
    )}
    {user && (
      <ExtendUserModal
        user={user}
        open={extendOpen}
        onClose={() => setExtendOpen(false)}
        onChanged={onChanged}
      />
    )}

    {/* Nested inside the detail modal on purpose: closing it (Esc / backdrop) returns
        to the user card rather than dismissing both. */}
    {user && (
      <UserEventsModal
        userID={user.id}
        userName={user.name}
        open={eventsOpen}
        onClose={() => setEventsOpen(false)}
      />
    )}
    {user && (
      <Modal
        open={msgOpen}
        onClose={() => setMsgOpen(false)}
        title={t('userDetail.messageTo', { name: user.name })}
      >
        <HtmlEditor
          value={msgText}
          onChange={setMsgText}
          rows={5}
          placeholder={t('userDetail.messagePlaceholder')}
        />
        <p className="mt-1 text-xs text-ink-muted">
          {msgMedia
            ? t('userDetail.captionLimit', { n: [...msgText].length })
            : `${[...msgText].length} / 4096`}
        </p>
        <div className="mt-3">
          <input
            ref={msgFileRef}
            type="file"
            className="hidden"
            onChange={(e) => setMsgMedia(e.target.files?.[0] ?? null)}
          />
          {msgMedia ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm text-ink">📎 {msgMedia.name}</span>
              <Button
                variant="subtle"
                size="xs"
                onClick={() => {
                  setMsgMedia(null)
                  if (msgFileRef.current) msgFileRef.current.value = ''
                }}
              >
                {t('userDetail.removeAttachment')}
              </Button>
            </div>
          ) : (
            <Button
              variant="light"
              size="sm"
              onClick={() => msgFileRef.current?.click()}
            >
              {t('userDetail.attachFile')}
            </Button>
          )}
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={() => setMsgOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button
            loading={sending}
            disabled={
              (!msgText.trim() && !msgMedia) ||
              [...msgText].length > (msgMedia ? 1024 : 4096)
            }
            onClick={async () => {
              setSending(true)
              try {
                await messageUser(user.id, msgText.trim(), msgMedia)
                setMsgText('')
                setMsgMedia(null)
                setMsgOpen(false)
                notifySuccess(t('userDetail.messageSent'))
              } catch (e) {
                notifyError(errMessage(e))
              } finally {
                setSending(false)
              }
            }}
          >
            {t('userDetail.send')}
          </Button>
        </div>
      </Modal>
    )}
    {confirmNode}
    </>
  )
}

// GroupChip is one access group in the user drawer. A solid chip ("on") is a group the
// user belongs to — clicking it (the ×) leaves; a dashed chip ("add") is one they can
// join — clicking (the ＋) adds. `count` is how many connections the group grants, shown
// so an operator can tell a rich group from an empty (access-revoking) one at a glance.
// TAG_MAX_LEN mirrors model.MaxUserTagLen for the hint text; the server is the one
// that enforces it.
const TAG_MAX_LEN = 32

// NoteAndTags is the operator's own annotation of the account: a free-text note and
// the tag list the user list filters on. Tags save on every change — a cheap write
// with no Xray reload — while the note, being typed rather than picked, saves on a
// button so half a sentence never lands in the journal.
function NoteAndTags({ user, onChanged }: { user: User; onChanged: () => void }) {
  const { t } = useTranslation()
  const [note, setNote] = useState(user.note ?? '')
  const [known, setKnown] = useState<TagCount[]>([])
  const { busy, run } = useAction()
  const tags = user.tags ?? []
  const tagKey = tags.join(',')

  useEffect(() => {
    setNote(user.note ?? '')
  }, [user.id, user.note])

  // Every tag in use, as suggestions — so the second user tagged "vip" gets the
  // same spelling as the first without retyping it. Refetched when this user's
  // tags change, since that is when the set of known tags can grow.
  useEffect(() => {
    let alive = true
    listUserTags()
      .then((l) => alive && setKnown(l))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user.id, tagKey])

  const saved = user.note ?? ''
  const noteDirty = note.trim() !== saved.trim()
  const saveNote = () =>
    run(async () => {
      await setUserNote(user.id, note)
      onChanged()
      notifySuccess(t('userDetail.noteSaved'))
    })
  const saveTags = (next: string[]) =>
    run(async () => {
      await setUserTags(user.id, next)
      onChanged()
    })

  return (
    <div className="flex flex-col gap-2.5">
      <TagsInput
        label={t('userDetail.tags')}
        value={tags}
        onChange={saveTags}
        options={known.map((k) => ({ value: k.tag, label: k.tag }))}
        hint={t('userDetail.tagsHint', { maxLen: TAG_MAX_LEN })}
      />
      <div>
        <Textarea
          label={t('userDetail.note')}
          value={note}
          onChange={setNote}
          rows={2}
          placeholder={t('userDetail.notePlaceholder')}
        />
        {noteDirty && (
          <div className="mt-1.5 flex justify-end gap-2">
            <Button
              size="sm"
              variant="light"
              color="gray"
              onClick={() => setNote(saved)}
              disabled={busy}
            >
              {t('common.cancel')}
            </Button>
            <Button size="sm" loading={busy} onClick={saveNote}>
              {t('common.save')}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

function GroupChip({
  name,
  count,
  state,
  onClick,
}: {
  name: string
  count: number
  state: 'on' | 'add'
  onClick: () => void
}) {
  const on = state === 'on'
  return (
    <button
      type="button"
      onClick={onClick}
      title={`${i18n.t('userDetail.nConnections', { count })} · ${i18n.t(on ? 'userDetail.removeFromGroup' : 'userDetail.addToGroup')}`}
      className={
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition ' +
        // Selected uses the theme-composited accent tint (translucent → readable on a
        // dark surface too), NOT a fixed bg-brand-NN shade, which would bake a light
        // fill that stays light in the dark theme. Mirrors the Checkbox checked state.
        (on
          ? 'border-accent accent-tint text-accent hover:border-brand-500'
          : 'border-dashed border-gray-300 bg-white text-ink-muted hover:border-brand-400 hover:text-accent')
      }
    >
      {!on && (
        <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <path d="M6 2v8M2 6h8" />
        </svg>
      )}
      <span className="max-w-40 truncate">{name}</span>
      <span className="opacity-70">· {count}</span>
      {on && (
        <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <path d="M3 3l6 6M9 3l-6 6" />
        </svg>
      )}
    </button>
  )
}
