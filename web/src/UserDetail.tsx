import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { QRCodeSVG } from 'qrcode.react'
import {
  banIP,
  MAX_DEVICE_LIMIT,
  deleteUser,
  genUserTelegramLink,
  getBilling,
  getStatsSeries,
  getUserConnections,
  getUserDevices,
  getUserHappLink,
  unbanIP,
  unbindUserDevice,
  resetUserTraffic,
  rotateSubToken,
  messageUser,
  unlinkUserTelegram,
  setResetPeriod,
  setUserEnabled,
  setUserLimits,
  setUserPlan,
  setUserGroups,
  listGroups,
  type Connection,
  type DailyPoint,
  type DeviceList,
  type Group,
  type TariffPlan,
  type User,
} from './api'
import {
  dateToUnixEndOfDay,
  deviceLimitOptions,
  fmtBytes,
  fmtDuration,
  fmtExpire,
  fmtLastSeen,
  fmtQuota,
  fmtSpeed,
  fmtTerm,
  gbToBytes,
  groupSpeedCap,
  isOnline,
  localDay,
  quotaOptions,
  ranges,
  resetPeriods,
  speedLimitOptions,
  termModes,
  unixToLocalDate,
} from './format'
import { useShowMore } from './hooks'
import { HtmlEditor } from './HtmlEditor'
import { errMessage, notifyError, notifySuccess } from './notify'
import { TrafficArea } from './charts'
import { NodeTrafficSplit } from './NodeTrafficSplit'
import { ABUSE_WINDOW_DAYS, AbuseList } from './AbuseList'
import { UserEventsModal } from './UserEventsModal'
import { inPanelTz } from './tz'
import {
  Button,
  cn,
  Code,
  CustomizableSelect,
  DatePicker,
  Drawer,
  IconButton,
  IconCalendar,
  IconCheck,
  IconBan,
  IconClose,
  IconUnlock,
  IconCopy,
  IconExternal,
  IconKey,
  IconPencil,
  IconRestart,
  IconSend,
  IconTable,
  IconTrash,
  Modal,
  Mono,
  Panel,
  ReadOnly,
  SegmentedControl,
  Select,
  SettingRow,
  ShowMore,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
} from './ui'
import i18n from './i18n'
import { useCan } from './role'
import { ExtendUserModal, RenameModal } from './UserModals'
import { GroupChip, NoteAndTags } from './UserNotes'

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




// StateRow is one fact about the account: what it is on the left, muted; what it
// says on the right, mono when it is a number. Rows divide; they are not boxed.
// One read-only fact about the account, in the row shape the settings screens use.
function StateRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <SettingRow
      label={label}
      control={<span className="text-xs text-ink">{children}</span>}
    />
  )
}

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
  // The address a ban or unban is in flight for, so a second click cannot race it.
  const [banBusy, setBanBusy] = useState<string | null>(null)
  // The user the card shows now: a ban's reply must not write one user's addresses
  // into the card after it switched to another.
  const shownUser = useRef<number | undefined>(undefined)
  shownUser.current = user?.id
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
  // The manual term: an end date, or a number of days starting on the first connection.
  const [dTerm, setDTerm] = useState('date')
  const [dHoldDays, setDHoldDays] = useState('30')
  // Whether the days were typed. Until they are, a pending term keeps its exact
  // seconds — one set through the API need not be whole days, and rounding it for the
  // field must not rewrite it on an unrelated save.
  const [dHoldEdited, setDHoldEdited] = useState(false)
  const [dLimitGb, setDLimitGb] = useState('0')
  const [dDeviceLimit, setDDeviceLimit] = useState('0')
  const [dSpeedLimit, setDSpeedLimit] = useState('0')
  const [dReset, setDReset] = useState('none')
  const [savingLimits, setSavingLimits] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [extendOpen, setExtendOpen] = useState(false)
  const email = useCopy()
  const happCopy = useCopy()
  const { confirm, confirmNode } = useConfirm()
  const canManage = useCan('users.manage')
  const canDelete = useCan('users.delete')
  // The ban routes need security.manage; can_ban already says whether the address may be banned.
  const canBan = useCan('security.manage')
  // The encrypted Happ link is asked for on its own — each one is an RSA encryption,
  // too dear to carry in the user list — and exists only while the operator has it
  // switched on: "" hides the row. A rotated token is a new address, so a new link.
  const [happLink, setHappLink] = useState('')
  const userId = user?.id
  const subUrl = user?.sub_url

  // biome-ignore lint/correctness/useExhaustiveDependencies: resets the card for a new user; resetLimitDraft is defined below and closes over `user`, so re-running it on a user change is the whole point
  useEffect(() => {
    setTgLink(null) // a one-time bind link is per-user; don't leak it across switches
    setEventsOpen(false) // ditto for the journal — never show one user's trail over another
    setRenaming(false)
    setExtendOpen(false)
    setSel(new Set((user?.groups ?? []).map((g) => g.id)))
    setGroupQuery('')
    resetLimitDraft()
  }, [user])

  useEffect(() => {
    setHappLink('')
    if (!userId || !subUrl) return
    let alive = true
    getUserHappLink(userId)
      .then((d) => alive && setHappLink(d.link))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [userId, subUrl])

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
    const holding = !!user && user.expire_at === 0 && (user.hold_seconds ?? 0) > 0
    setDTerm(holding ? 'hold' : 'date')
    setDHoldDays(holding && user ? String(Math.floor(user.hold_seconds / 86400)) : '30')
    setDHoldEdited(false)
    setDLimitGb(user && user.data_limit ? String(user.data_limit / (1024 * 1024 * 1024)) : '0')
    setDDeviceLimit(String(user?.device_limit ?? 0))
    setDSpeedLimit(String(user?.speed_limit ?? 0))
    setDReset(user?.reset_period || 'none')
  }

  const planManaged = billingOn && dPlan !== '0'
  // A group that sets a speed cap overrides the one below, whatever the tariff says —
  // unless a blocklist throttle is stricter, which nothing loosens.
  const groupCapRaw = groupSpeedCap(user?.groups)
  const throttledBelow =
    !!user &&
    user.abuse_action === 'throttle' &&
    user.speed_limit > 0 &&
    !!groupCapRaw &&
    user.speed_limit < groupCapRaw.kbps
  const groupCap = throttledBelow ? null : groupCapRaw
  // Whether the account is waiting for its first connection right now, and what the
  // draft says its term should be.
  const heldNow = !!user && user.expire_at === 0 && (user.hold_seconds ?? 0) > 0
  const dHoldSeconds =
    heldNow && user && !dHoldEdited
      ? user.hold_seconds
      : Math.floor(Number(dHoldDays) || 0) * 86400
  const termDirty =
    user != null &&
    (dTerm === 'hold'
      ? !heldNow || dHoldSeconds !== user.hold_seconds
      : heldNow || dExpire !== unixToLocalDate(user.expire_at))

  const limitsDirty =
    user != null &&
    (dPlan !== String(user.plan_id || 0) ||
      (!planManaged &&
        (termDirty ||
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
        // The term only when it was edited: an untouched one is left to the server,
        // which may know better by now. A hold goes with no date, a date with no hold.
        await setUserLimits(user.id, {
          data_limit: gbToBytes(Number(dLimitGb)),
          device_limit: Number(dDeviceLimit),
          // Only when changed: the server reads a speed it is sent as the operator
          // overruling a blocklist throttle, and a quota save is not that.
          speed_limit:
            Number(dSpeedLimit) !== (user.speed_limit ?? 0) ? Number(dSpeedLimit) : undefined,
          term: termDirty
            ? {
                expire_at: dTerm === 'hold' ? 0 : dateToUnixEndOfDay(dExpire),
                hold_seconds: dTerm === 'hold' ? dHoldSeconds : 0,
                seen_expire_at: user.expire_at,
                seen_hold_seconds: user.hold_seconds ?? 0,
              }
            : undefined,
        })
        if (dReset !== (user.reset_period || 'none')) await setResetPeriod(user.id, dReset)
      }
      onChanged()
      notifySuccess(t('common.saved'))
    } catch (e) {
      fail(e)
      // A refused term means the card is out of date; bring it up to the server's.
      onChanged()
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
  // A user whose selected groups limit access but grant nothing between them sees no
  // connections at all — a silent lockout (a group whose grants were swept still
  // limits). Groups that do not limit access take no part. Warn before it's applied.
  const limitingSelected = allGroups.filter((g) => sel.has(g.id) && g.limits_access)
  const selectedGrantCount = limitingSelected.reduce((n, g) => n + (g.grants?.length ?? 0), 0)
  const groupsLockOut = limitingSelected.length > 0 && selectedGrantCount === 0
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

  // A ban drops the address on every server until it is lifted, for everyone behind
  // it — hence the confirmation. Unbanning lifts every ban on it, whatever placed it.
  const banConn = async (ip: string) => {
    if (!user) return
    const ok = await confirm({
      title: t('userDetail.banTitle', { ip }),
      body: t('userDetail.banBody'),
      confirmLabel: t('userDetail.ban'),
      danger: true,
    })
    if (!ok) return
    await changeBan(ip, user.id, () => banIP(ip, user.id))
  }
  const unbanConn = async (ip: string) => {
    if (!user) return
    await changeBan(ip, user.id, () => unbanIP(ip))
  }
  const changeBan = async (ip: string, id: number, change: () => Promise<unknown>) => {
    if (banBusy) return
    setBanBusy(ip)
    try {
      await change()
      const fresh = await getUserConnections(id)
      if (shownUser.current === id) setConns(fresh)
    } catch (e) {
      fail(e)
    } finally {
      setBanBusy(null)
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
              type="button"
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
                { when: new Date(user.abuse_until * 1000).toLocaleString(i18n.language, inPanelTz()) },
              )}
            </p>
          )}

          {/* 2. What is done to an account most often, and then what is done to the
                 account itself. Icons with their word in the title, like every other
                 row of actions in the panel. */}
          <div className="flex flex-wrap items-center gap-1">
            <IconButton
              variant="filled"
              color="brand"
              title={t('userDetail.subLink')}
              href={user.sub_url}
              target="_blank"
            >
              <IconExternal />
            </IconButton>
            {canManage && (
            <>
            <IconButton title={t('usersPanel.extend')} onClick={() => setExtendOpen(true)}>
              <IconCalendar size={16} />
            </IconButton>
            <IconButton
              title={t('usersPanel.resetTraffic')}
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
              <IconRestart />
            </IconButton>
            <IconButton title={t('userDetail.rename')} onClick={() => setRenaming(true)}>
              <IconPencil />
            </IconButton>
            <IconButton
              title={t('userDetail.rotate')}
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
              <IconKey />
            </IconButton>
            </>
            )}
            <IconButton title={t('events.title')} onClick={() => setEventsOpen(true)}>
              <IconTable />
            </IconButton>
            {canDelete && (
            <IconButton
              color="red"
              title={t('userDetail.deleteUser')}
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
              <IconTrash />
            </IconButton>
            )}
          </div>

          {/* 3. State: what the account is right now. The switch applies at once —
                 it is a switch; everything else here is read-only. */}
          <ReadOnly when={!canManage}>
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
              <Mono>{fmtTerm(user.expire_at, user.hold_seconds)}</Mono>
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
              {!(user.groups ?? []).some((g) => g.limits_access) ? (
                <span className="text-ink-muted">{t('userDetail.allConnections')}</span>
              ) : (
                <span className="text-accent">
                  {(user.groups ?? []).map((g) => g.name).join(', ')}
                </span>
              )}
            </StateRow>
          </Panel>
          </ReadOnly>

          {/* 4. Devices: the addresses the account connects from, always. A router or
                 any client that sends no HWID shows up only here, so this list must not
                 give way to the bound installs below. */}
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
            {conns.length === 0 ? (
              <p className="px-3.5 py-3 text-xs text-ink-muted">
                {t('userDetail.noConnections')}
              </p>
            ) : (
              <>
                {devices.shown.map((c) => (
                  <div
                    key={c.ip}
                    className={cn(
                      'flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0',
                      c.banned && 'bg-gray-50',
                    )}
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <span
                        className={cn(
                          'size-1.5 shrink-0 rounded-full',
                          c.banned
                            ? 'bg-gray-300'
                            : isOnline(c.last_seen)
                              ? 'bg-success'
                              : 'bg-gray-400',
                        )}
                      />
                      <Mono
                        className={cn('truncate text-xs', c.banned ? 'text-ink-muted' : 'text-ink')}
                      >
                        {c.ip}
                      </Mono>
                    </span>
                    <span className="flex shrink-0 items-center gap-2">
                      <Mono
                        className="text-[11px] text-ink-muted"
                        title={`${t('userDetail.lastConnect')}\n${t('userDetail.approxHint', {
                          time: fmtDuration(c.approx_seconds),
                          count: c.count,
                        })}`}
                      >
                        {fmtLastSeen(c.last_seen)}
                      </Mono>
                      {/* One slot on every row, button or not, so the rows keep one
                          height and the times one column; the negative margin keeps the
                          button from making its row taller than a row of text. */}
                      {canBan && (
                        <span className="-my-1 flex size-6 shrink-0 items-center justify-center">
                          {c.banned ? (
                            <IconButton
                              compact
                              disabled={banBusy !== null}
                              title={t('userDetail.unban')}
                              onClick={() => unbanConn(c.ip)}
                            >
                              <IconUnlock size={16} />
                            </IconButton>
                          ) : (
                            c.can_ban && (
                              <IconButton
                                compact
                                color="red"
                                disabled={banBusy !== null}
                                title={t('userDetail.ban')}
                                onClick={() => banConn(c.ip)}
                              >
                                <IconBan size={16} />
                              </IconButton>
                            )
                          )}
                        </span>
                      )}
                    </span>
                  </div>
                ))}
                {devices.rest > 0 && (
                  <div className="px-3.5 py-2">
                    <ShowMore rest={devices.rest} onClick={devices.showMore} />
                  </div>
                )}
              </>
            )}
          </Panel>

          {/* 4b. Bound installs, when HWID binding is on: the apps that fetched the
                 subscription with an HWID, each of which can be unbound. */}
          {bound?.enabled && (
            <ReadOnly when={!canManage}>
            <Panel
              title={t('userDetail.boundDevices')}
              aside={
                <span className="text-xs text-ink-muted">
                  {bound.limit > 0
                    ? t('userDetail.boundOfLimit', {
                        count: bound.devices.length,
                        limit: bound.limit,
                      })
                    : t('userDetail.boundTotal', { count: bound.devices.length })}
                </span>
              }
            >
              {bound.devices.length === 0 ? (
                <p className="px-3.5 py-3 text-xs text-ink-muted">
                  {t('userDetail.noBoundDevices')}
                </p>
              ) : (
                bound.devices.map((d) => {
                  // No address here: it is where the subscription was last fetched
                  // from, which the list of addresses above tells better.
                  const os = [d.os, d.os_version].filter(Boolean).join(' ')
                  return (
                    <div
                      key={d.hwid}
                      className="flex items-center justify-between gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0"
                    >
                      <span className="flex min-w-0 flex-col" title={d.hwid}>
                        {/* The identifier itself first: it is what the binding is on,
                            and what an operator matches against a user's report. */}
                        <Mono className="truncate text-xs text-ink">{d.hwid}</Mono>
                        {[d.model, os].filter(Boolean).length > 0 && (
                          <span className="truncate text-xs text-ink-muted">
                            {[d.model, os].filter(Boolean).join(' · ')}
                          </span>
                        )}
                      </span>
                      <span className="flex shrink-0 items-center gap-3">
                        <Mono className="text-[11px] text-ink-muted" title={t('userDetail.lastSubFetch')}>
                          {fmtLastSeen(d.last_seen)}
                        </Mono>
                        <IconButton
                          color="red"
                          title={t('userDetail.unbind')}
                          onClick={() => unbindDevice(d.hwid)}
                        >
                          <IconClose size={16} />
                        </IconButton>
                      </span>
                    </div>
                  )
                })
              )}
              {bound.devices.length > 0 && (
                <div className="border-t border-gray-100 px-3.5 py-2">
                  <Button variant="subtle" color="red" size="xs" onClick={() => unbindDevice()}>
                    {t('userDetail.unbindAll')}
                  </Button>
                </div>
              )}
            </Panel>
            </ReadOnly>
          )}

          {/* 5. Tariff and limits. A tariff owns the quota, the device cap and the
                 reset cycle, so under one the fields are shown disabled rather than
                 hidden: the operator sees what the plan set, and why they cannot
                 edit it. Fields are a draft until Save. */}
          <ReadOnly when={!canManage}>
          <Panel title={t('userDetail.planAndLimits')}>
            {billingOn && (
              <SettingRow
                label={t('userDetail.plan')}
                hint={planManaged ? t('userDetail.planHint') : undefined}
                field={
                  <Select
                    data={planSelectData(plans, user)}
                    value={dPlan}
                    onChange={setDPlan}
                  />
                }
              />
            )}
            <SettingRow
              label={t('usersPanel.termMode')}
              field={
                <Select
                  data={termModes()}
                  value={dTerm}
                  onChange={setDTerm}
                  disabled={planManaged}
                />
              }
            />
            {dTerm === 'hold' && !planManaged ? (
              <SettingRow
                label={t('usersPanel.holdDays')}
                hint={t('userDetail.holdHint')}
                field={
                  <TextInput
                    type="number"
                    value={dHoldDays}
                    onChange={(v) => {
                      setDHoldDays(v.replace(/\D/g, ''))
                      setDHoldEdited(true)
                    }}
                  />
                }
              />
            ) : (
              <SettingRow
                label={t('usersPanel.validUntil')}
                field={
                  <DatePicker value={dExpire} onChange={setDExpire} disabled={planManaged} />
                }
              />
            )}
            <SettingRow
              label={t('usersPanel.trafficLimit')}
              field={
                <Select
                  data={quotaData}
                  value={dLimitGb}
                  onChange={setDLimitGb}
                  disabled={planManaged}
                />
              }
            />
            <SettingRow
              label={t('userDetail.deviceLimit')}
              hint={t('userDetail.deviceLimitHint')}
              field={
                <CustomizableSelect
                  data={deviceLimitOptions()}
                  value={dDeviceLimit}
                  format={(n) => t('devices.count', { count: n })}
                  max={MAX_DEVICE_LIMIT}
                  onChange={setDDeviceLimit}
                  disabled={planManaged}
                />
              }
            />
            <SettingRow
              label={t('userDetail.speedLimit')}
              hint={
                groupCap ? (
                  <span className="text-warning">
                    {t('userDetail.groupSpeedInForce', {
                      name: groupCap.name,
                      speed: fmtSpeed(groupCap.kbps),
                    })}
                  </span>
                ) : (
                  t('userDetail.speedLimitHint')
                )
              }
              field={
                <CustomizableSelect
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
              }
            />
            <SettingRow
              label={t('usersPanel.autoReset')}
              field={
                <Select
                  data={resetPeriods()}
                  value={dReset}
                  onChange={setDReset}
                  disabled={planManaged}
                />
              }
            />
            {limitsDirty && (
              <SettingRow
                control={
                  <span className="flex gap-2">
                    <Button
                      size="xs"
                      variant="light"
                      color="gray"
                      onClick={resetLimitDraft}
                      disabled={savingLimits}
                    >
                      {t('common.cancel')}
                    </Button>
                    <Button
                      size="xs"
                      loading={savingLimits}
                      disabled={!planManaged && dTerm === 'hold' && dHoldSeconds <= 0}
                      onClick={saveLimitDraft}
                    >
                      {t('common.save')}
                    </Button>
                  </span>
                }
              />
            )}
          </Panel>
          </ReadOnly>

          {/* 6. The operator's own annotation of the account. */}
          <ReadOnly when={!canManage}>
          <Panel title={t('userDetail.noteAndTags')}>
            <NoteAndTags user={user} onChanged={onChanged} />
          </Panel>
          </ReadOnly>

          {/* Access groups: which connections this account may use. Applied on a
              button because each save reconciles Xray — several toggles should be
              one restart, not several. */}
          {allGroups.length > 0 && (
            <ReadOnly when={!canManage}>
            <Panel
              title={t('groups.title')}
              aside={
                <span className="text-[11px] text-ink-muted">
                  {sel.size === 0
                    ? t('userDetail.allConnections')
                    : t('groups.nSelected', { count: sel.size })}
                </span>
              }
            >
              <SettingRow>
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
                  <p className="text-[11px] text-ink-muted">{t('userDetail.noGroups')}</p>
                )}
              </SettingRow>

              {(availableGroups.length > 0 || groupQ) && (
                <SettingRow label={t('userDetail.addToGroup')}>
                  <div className="flex flex-col gap-1.5">
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
                      <p className="text-[11px] text-ink-muted">{t('common.nothingFound')}</p>
                    )}
                  </div>
                </SettingRow>
              )}

              {groupsLockOut && (
                <SettingRow
                  hint={
                    <span className="text-warning">{t('userDetail.groupsGrantNothing')}</span>
                  }
                />
              )}

              {groupsDirty && (
                <SettingRow
                  control={
                    <span className="flex gap-2">
                      <Button
                        size="xs"
                        variant="light"
                        color="gray"
                        onClick={resetGroups}
                        disabled={savingGroups}
                      >
                        {t('common.cancel')}
                      </Button>
                      <Button size="xs" loading={savingGroups} onClick={applyGroups}>
                        {t('common.apply')}
                      </Button>
                    </span>
                  }
                />
              )}
            </Panel>
            </ReadOnly>
          )}

          {/* The subscription itself: the QR an operator hands over, and the link. */}
          <Panel title={t('usersPanel.subscription')} pad bodyClassName="flex flex-col gap-3">
            <div className="flex justify-center">
              <div className="rounded-lg bg-onaccent p-3">
                <QRCodeSVG value={user.sub_url} size={168} />
              </div>
            </div>
            <Code block copy>{user.sub_url}</Code>
            {happLink && (
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs text-ink-muted">{t('userDetail.happLink')}</span>
                <Button size="xs" variant="light" onClick={() => happCopy.copy(happLink)}>
                  {t(happCopy.copied ? 'common.copied' : 'common.copy')}
                </Button>
              </div>
            )}
          </Panel>

          <ReadOnly when={!canManage}>
          <Panel title="Telegram">
            {user.telegram_linked ? (
              <>
                <SettingRow
                  label={t('userDetail.botLinked')}
                  control={
                    <span className="flex items-center gap-2">
                      {/* A broadcast to one person. Shown only with a linked chat AND a
                          running user bot — it is the bot that delivers, so without it
                          the button could only ever produce an error. */}
                      {userBotEnabled && (
                        <IconButton
                          title={t('userDetail.sendMessage')}
                          onClick={() => setMsgOpen(true)}
                        >
                          <IconSend />
                        </IconButton>
                      )}
                      <IconButton
                        color="red"
                        title={t('userDetail.unlink')}
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
                        <IconClose size={16} />
                      </IconButton>
                    </span>
                  }
                />
                {!!user.tg_chat_id && (
                  <SettingRow
                    label="Telegram ID"
                    control={<Code copy>{String(user.tg_chat_id)}</Code>}
                  />
                )}
              </>
            ) : user.telegram_link ? (
              <SettingRow
                hint={
                  tgLink ? t('userDetail.linkNote', { mins: tgLink.mins }) : t('userDetail.linkHint')
                }
                control={
                  <IconButton
                    title={t('userDetail.getLink')}
                    onClick={() =>
                      genUserTelegramLink(user.id)
                        .then((r) =>
                          setTgLink({ url: r.deep_link, mins: Math.round(r.expires_sec / 60) }),
                        )
                        .catch(fail)
                    }
                  >
                    <IconKey />
                  </IconButton>
                }
              >
                {tgLink && <Code block copy>{tgLink.url}</Code>}
              </SettingRow>
            ) : (
              <SettingRow hint={t('userDetail.enableUserBot')} />
            )}
          </Panel>
          </ReadOnly>

          <Panel
            title={t('stats.blocklistMatches')}
            aside={
              <span className="text-xs text-ink-muted">
                {t('stats.window', { count: ABUSE_WINDOW_DAYS })}
              </span>
            }
          >
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
