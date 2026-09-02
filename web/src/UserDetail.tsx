import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { QRCodeSVG } from 'qrcode.react'
import {
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
  fmtBytes,
  fmtExpire,
  fmtLastSeen,
  fmtQuota,
  fmtSpeed,
  gbToBytes,
  isOnline,
  localDay,
  deviceLimitOptions,
  quotaOptions,
  speedLimitOptions,
  ranges,
  resetPeriods,
  statusInfo,
} from './format'
import { useAction, useShowMore } from './hooks'
import { HtmlEditor } from './HtmlEditor'
import { errMessage, notifyError, notifySuccess } from './notify'
import { TrafficArea } from './charts'
import { NodeTrafficSplit } from './NodeTrafficSplit'
import { AbuseList } from './AbuseList'
import { UserEventsModal } from './UserEventsModal'
import {
  Badge,
  Button,
  Code,
  DatePicker,
  Divider,
  Modal,
  IconCheck,
  IconClose,
  IconCopy,
  IconPencil,
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

function unixToDate(unix: number): string {
  return unix ? new Date(unix * 1000).toISOString().slice(0, 10) : ''
}

// optLabel resolves a select value to its human label, for the confirmation text.
function optLabel(data: { value: string; label: string }[], value: string): string {
  return data.find((o) => o.value === value)?.label ?? value
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

// EditableName renders the user's name with a pencil; clicking it swaps to an
// inline input with save/cancel. Used as the modal title.
function EditableName({ user, onChanged }: { user: User; onChanged: () => void }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(user.name)
  const { busy, run } = useAction()

  useEffect(() => {
    setDraft(user.name)
    setEditing(false)
  }, [user.id, user.name])

  const save = async () => {
    const name = draft.trim()
    if (!name || name === user.name) {
      setEditing(false)
      return
    }
    run(async () => {
      await renameUser(user.id, name)
      onChanged()
      setEditing(false)
    })
  }

  if (!editing) {
    return (
      <span className="flex h-8 min-w-0 items-center gap-2">
        <span className="truncate">{user.name}</span>
        <button
          onClick={() => {
            setDraft(user.name)
            setEditing(true)
          }}
          className="shrink-0 text-gray-400 transition hover:text-accent"
          title={i18n.t('userDetail.rename')}
        >
          <IconPencil size={16} />
        </button>
      </span>
    )
  }
  return (
    <span className="flex h-8 min-w-0 items-center gap-1.5">
      <input
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          else if (e.key === 'Escape') setEditing(false)
        }}
        className="h-8 min-w-0 flex-1 rounded-md border border-gray-300 px-2 text-base font-bold text-ink outline-none focus:border-brand-500 focus:ring-2 focus:ring-brand-100"
      />
      <button
        onClick={save}
        disabled={busy}
        title={i18n.t('common.save')}
        className="shrink-0 text-success transition hover:text-success disabled:opacity-50"
      >
        <IconCheck size={18} />
      </button>
      <button
        onClick={() => setEditing(false)}
        title={i18n.t('common.cancel')}
        className="shrink-0 text-gray-400 transition hover:text-gray-600"
      >
        <IconClose size={18} />
      </button>
    </span>
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
  // Bound installs (HWID). Null until the first load, and left empty when the
  // operator hasn't switched device binding on — the whole block then stays hidden.
  const [bound, setBound] = useState<DeviceList | null>(null)
  const [range, setRange] = useState('30')
  const [limitGb, setLimitGb] = useState('0')
  const [deviceLimit, setDeviceLimit] = useState('0')
  const [speedLimit, setSpeedLimit] = useState('0')
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
  const email = useCopy()
  const { confirm, confirmNode } = useConfirm()

  useEffect(() => {
    setLimitGb(user && user.data_limit ? String(user.data_limit / (1024 * 1024 * 1024)) : '0')
    setDeviceLimit(user ? String(user.device_limit ?? 0) : '0')
    setSpeedLimit(user ? String(user.speed_limit ?? 0) : '0')
    setTgLink(null) // a one-time bind link is per-user; don't leak it across switches
    setEventsOpen(false) // ditto for the journal — never show one user's trail over another
    setSel(new Set((user?.groups ?? []).map((g) => g.id)))
    setGroupQuery('')
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
  const speedData = speedLimitOptions().some((o) => o.value === speedLimit)
    ? speedLimitOptions()
    : [...speedLimitOptions(), { value: speedLimit, label: fmtSpeed(Number(speedLimit)) }]

  const quotaData = user
    ? quotaOptions().some((o) => o.value === limitGb)
      ? quotaOptions()
      : [...quotaOptions(), { value: limitGb, label: fmtBytes(user.data_limit) }]
    : quotaOptions()

  const saveLimits = (dl: number, ea: number, dev: number, speed?: number) =>
    setUserLimits(user!.id, dl, ea, dev, speed).then(onChanged).catch(fail)

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

  // confirmChange gates an edit in the management block. These controls apply
  // to a live subscription the moment they're touched, so a misclick would
  // otherwise silently change what the user is paying for.
  const confirmChange = async (field: string, from: string, to: string, apply: () => void) => {
    const ok = await confirm({
      title: t('userDetail.changeTitle'),
      body: t('userDetail.changeBody', { field, name: user!.name, from, to }),
      confirmLabel: t('common.edit'),
    })
    if (ok) apply()
  }

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

  // A tariff owns the quota, the device cap and the reset cycle: applying or
  // renewing one overwrites all three at once (planWriteFor, core/manager_billing.go),
  // so editing them by hand here would only hold until the next payment. Under a
  // plan the inputs are replaced by a read-only summary; "manual" brings them back.
  const planManaged = billingOn && !!user?.plan_id

  return (
    <>
    <Modal
      open={!!user}
      onClose={onClose}
      size="xl"
      title={user ? <EditableName user={user} onChanged={onChanged} /> : undefined}
    >
      {user && (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap gap-2">
            <Badge color={statusInfo(user.status).color as never}>{statusInfo(user.status).label}</Badge>
            <Badge color={isOnline(user.last_seen) ? 'greenSolid' : 'gray'}>
              {isOnline(user.last_seen)
                ? t('usersPanel.online')
                : `${t('usersPanel.offline')} · ${fmtLastSeen(user.last_seen)}`}
            </Badge>
            <Badge color="brand">{fmtQuota(user.used_up + user.used_down, user.data_limit)}</Badge>
            {user.expire_at > 0 && (
              <Badge color="gray">{t('usersPanel.until', { date: fmtExpire(user.expire_at) })}</Badge>
            )}
            {user.device_limit > 0 && (
              <Badge color={user.status === 'device_limited' ? 'orange' : 'gray'}>
                {t('userDetail.devicesOf', { active: user.active_devices, limit: user.device_limit })}
              </Badge>
            )}
          </div>

          <div className="flex items-center gap-2 text-sm text-ink-muted">
            <span className="shrink-0">{t('userDetail.systemId')}</span>
            <Code>{user.system_email}</Code>
            <button
              onClick={() => email.copy(user.system_email)}
              className="text-gray-400 transition hover:text-gray-600"
              title={t('common.copy')}
            >
              {email.copied ? <IconCheck /> : <IconCopy />}
            </button>
          </div>

          <NoteAndTags user={user} onChanged={onChanged} />

          <Divider label={t('userDetail.management')} />
          <div className="flex items-center justify-between">
            <span className="text-sm">
              {t(user.enabled ? 'userDetail.subOn' : 'userDetail.subOff')}
              {user.abuse_action && user.abuse_until && (
                <span className="mt-0.5 block text-xs text-orange-600">
                  {t(
                    user.abuse_action === 'disable'
                      ? 'userDetail.abuseDisabled'
                      : 'userDetail.abuseThrottled',
                    { when: new Date(user.abuse_until * 1000).toLocaleString(i18n.language) },
                  )}
                </span>
              )}
            </span>
            <Switch
              checked={user.enabled}
              onChange={(v) =>
                confirmChange(
                  t('usersPanel.subscription'),
                  t(user.enabled ? 'userDetail.on' : 'userDetail.off'),
                  t(v ? 'userDetail.on' : 'userDetail.off'),
                  () => setUserEnabled(user.id, v).then(onChanged).catch(fail),
                )
              }
            />
          </div>

          {allGroups.length > 0 && (
            <div className="flex flex-col gap-2.5">
              <div className="flex items-center justify-between">
                <span className="text-sm">{t('groups.title')}</span>
                <Badge color={sel.size === 0 ? 'gray' : 'brand'} size="xs">
                  {sel.size === 0
                    ? t('userDetail.allConnections')
                    : t('groups.nSelected', { count: sel.size })}
                </Badge>
              </div>

              {/* Membership: solid chips are the groups the user is IN; click × to leave. */}
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
                <p className="text-xs text-ink-muted">
                  {t('userDetail.noGroups')}
                </p>
              )}

              {/* Add: dashed chips are groups the user can join; click ＋ to add. */}
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
                <div className="flex justify-end gap-2 pt-0.5">
                  <Button size="sm" variant="light" color="gray" onClick={resetGroups} disabled={savingGroups}>
                    {t('common.cancel')}
                  </Button>
                  <Button size="sm" loading={savingGroups} onClick={applyGroups}>
                    {t('common.apply')}
                  </Button>
                </div>
              )}
            </div>
          )}

          {billingOn && (
            <>
              <Select
                label={t('userDetail.plan')}
                data={planSelectData(plans, user)}
                value={String(user.plan_id || 0)}
                onChange={(v) =>
                  confirmChange(
                    t('userDetail.plan'),
                    optLabel(planSelectData(plans, user), String(user.plan_id || 0)),
                    optLabel(planSelectData(plans, user), v),
                    () => setUserPlan(user.id, Number(v)).then(onChanged).catch(fail),
                  )
                }
              />
              <p className="-mt-1 text-xs text-ink-muted">
                {t('userDetail.planHint')}
              </p>
            </>
          )}

          <DatePicker
            label={t('usersPanel.validUntil')}
            value={unixToDate(user.expire_at)}
            onChange={(v) => {
              const ea = v ? Math.floor(new Date(v).getTime() / 1000) : 0
              confirmChange(t('usersPanel.validUntil'), dateLabel(user.expire_at), dateLabel(v), () =>
                saveLimits(user.data_limit, ea, user.device_limit),
              )
            }}
          />

          {planManaged ? (
            <div className="rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2 text-xs text-ink-muted">
              {t('userDetail.planLimitsPrefix')}{' '}
              <span className="text-ink">
                {user.data_limit > 0 ? fmtBytes(user.data_limit) : t('userDetail.noLimit')}
              </span>
              {t('userDetail.planLimitsDevices')}{' '}
              <span className="text-ink">
                {user.device_limit > 0 ? user.device_limit : t('userDetail.noLimit')}
              </span>
              {user.speed_limit > 0 && (
                <>
                  {t('userDetail.planLimitsSpeed')}{' '}
                  <span className="text-ink">{fmtSpeed(user.speed_limit)}</span>
                </>
              )}
              {t('userDetail.planLimitsReset')}{' '}
              <span className="text-ink">{resetLabel(user.reset_period)}</span>.{' '}
              {t('userDetail.planLimitsSuffix')}
            </div>
          ) : (
            <>
              <Select
                label={t('usersPanel.trafficLimit')}
                data={quotaData}
                value={limitGb}
                onChange={(v) =>
                  confirmChange(
                    t('usersPanel.trafficLimit'),
                    optLabel(quotaData, limitGb),
                    optLabel(quotaData, v),
                    () => {
                      setLimitGb(v)
                      saveLimits(gbToBytes(Number(v)), user.expire_at, user.device_limit)
                    },
                  )
                }
              />
              <Select
                label={t('userDetail.deviceLimit')}
                data={deviceLimitOptions()}
                value={deviceLimit}
                onChange={(v) =>
                  confirmChange(
                    t('userDetail.deviceLimit'),
                    optLabel(deviceLimitOptions(), deviceLimit),
                    optLabel(deviceLimitOptions(), v),
                    () => {
                      setDeviceLimit(v)
                      saveLimits(user.data_limit, user.expire_at, Number(v))
                    },
                  )
                }
              />
              <p className="-mt-1 text-xs text-ink-muted">
                {t('userDetail.deviceLimitHint')}
              </p>
              <Select
                label={t('userDetail.speedLimit')}
                data={speedData}
                value={speedLimit}
                onChange={(v) =>
                  confirmChange(
                    t('userDetail.speedLimit'),
                    optLabel(speedData, speedLimit),
                    optLabel(speedData, v),
                    () => {
                      setSpeedLimit(v)
                      saveLimits(
                        user.data_limit,
                        user.expire_at,
                        user.device_limit,
                        Number(v),
                      )
                    },
                  )
                }
              />
              <p className="-mt-1 text-xs text-ink-muted">
                {t('userDetail.speedLimitHint')}
              </p>
              <Select
                label={t('usersPanel.autoReset')}
                data={resetPeriods()}
                value={user.reset_period || 'none'}
                onChange={(v) =>
                  confirmChange(
                    t('usersPanel.autoReset'),
                    optLabel(resetPeriods(), user.reset_period || 'none'),
                    optLabel(resetPeriods(), v),
                    () => setResetPeriod(user.id, v).then(onChanged).catch(fail),
                  )
                }
              />
            </>
          )}
          <Button variant="light" onClick={() => setEventsOpen(true)}>
            {t('events.title')}
          </Button>
          <Button
            color="orange"
            variant="light"
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
          <Button
            color="red"
            variant="light"
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

          <Divider label={t('usersPanel.subscription')} />
          <div className="flex justify-center">
            <div className="rounded-lg bg-onaccent p-3">
              <QRCodeSVG value={user.sub_url} size={200} />
            </div>
          </div>
          <Code block copy>{user.sub_url}</Code>
          <div className="flex flex-wrap gap-2">
            <Button size="xs" variant="light" href={user.sub_url} target="_blank">
              {t('usersPanel.openSub')}
            </Button>
            <Button
              size="xs"
              variant="light"
              color="orange"
              onClick={async () => {
                const ok = await confirm({
                  title: t('userDetail.rotateTitle'),
                  body:
                    t('userDetail.rotateBody'),
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
          </div>

          <Divider label="Telegram" />
          {user.telegram_linked ? (
            <div className="flex flex-col gap-2">
              <p className="text-sm text-success">{t('userDetail.botLinked')}</p>
              {!!user.tg_chat_id && (
                <p className="text-xs text-ink-muted">
                  Telegram ID: <Code copy>{String(user.tg_chat_id)}</Code>
                </p>
              )}
              {/* A broadcast to one person. Shown only with a linked chat AND a
                  running user bot — it is the bot that delivers, so without it the
                  button could only ever produce an error. */}
              {userBotEnabled && (
                  <Button size="xs" variant="light" onClick={() => setMsgOpen(true)}>
                    {t('userDetail.sendMessage')}
                  </Button>
              )}
              <Button
                size="xs"
                variant="light"
                color="orange"
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
          ) : user.telegram_link ? (
            <div className="flex flex-col gap-2">
              <p className="text-sm text-ink-muted">
                {t('userDetail.linkHint')}
              </p>
              <Button
                size="xs"
                variant="light"
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
            </div>
          ) : (
            <p className="text-sm text-ink-muted">
              {t('userDetail.enableUserBot')}
            </p>
          )}

          <Divider label={t('stats.blocklistMatches')} />
          <AbuseList userId={user.id} first={5} />

          <Divider label={t('userDetail.devices')} />
          <p className="text-sm text-ink-muted">
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
          </p>
          {conns.length === 0 ? (
            <p className="py-2 text-center text-sm text-ink-muted">{t('userDetail.noConnections')}</p>
          ) : (
            <div className="flex flex-col gap-1.5">
              {devices.shown.map((c) => (
                <div
                  key={c.ip}
                  className="flex items-center justify-between gap-2 rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    {isOnline(c.last_seen) ? (
                      <Badge color="greenSolid">{t('userDetail.onlineWord')}</Badge>
                    ) : (
                      <Badge color="gray">{t('usersPanel.offline')}</Badge>
                    )}
                    <span className="truncate font-mono text-sm">{c.ip}</span>
                  </div>
                  <span className="shrink-0 text-xs text-ink-muted">
                    {fmtLastSeen(c.last_seen)} · {c.count}×
                  </span>
                </div>
              ))}
              <ShowMore rest={devices.rest} onClick={devices.showMore} />
            </div>
          )}

          {bound?.enabled && (
            <>
              <Divider label={t('userDetail.boundDevices')} />
              <div className="flex items-center justify-between gap-2">
                <p className="text-sm text-ink-muted">
                  {bound.limit > 0
                    ? t('userDetail.boundOfLimit', {
                        count: bound.devices.length,
                        limit: bound.limit,
                      })
                    : t('userDetail.boundTotal', { count: bound.devices.length })}
                </p>
                {bound.devices.length > 0 && (
                  <Button
                    variant="subtle"
                    color="red"
                    size="xs"
                    onClick={() => unbindDevice()}
                  >
                    {t('userDetail.unbindAll')}
                  </Button>
                )}
              </div>
              {bound.devices.length === 0 ? (
                <p className="py-2 text-center text-sm text-ink-muted">
                  {t('userDetail.noBoundDevices')}
                </p>
              ) : (
                <div className="flex flex-col gap-1.5">
                  {bound.devices.map((d) => (
                    <div
                      key={d.hwid}
                      className="flex items-center justify-between gap-2 rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2"
                    >
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">
                          {d.model || d.os || d.hwid}
                        </div>
                        <div className="truncate text-xs text-ink-muted">
                          {[d.os, d.os_version, d.ip].filter(Boolean).join(' · ')}
                        </div>
                        {/* The id itself, because the line above doesn't identify
                            anything: two identical phones are two identical rows, and
                            "which one am I unbinding" has no answer without it. Full
                            value in the tooltip and in the DOM, so it can be copied
                            even though it's visually truncated. */}
                        <div
                          className="truncate font-mono text-[11px] text-ink-muted/70"
                          title={d.hwid}
                        >
                          {d.hwid}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <span className="text-xs text-ink-muted">{fmtLastSeen(d.last_seen)}</span>
                        <Button
                          variant="subtle"
                          color="red"
                          size="xs"
                          onClick={() => unbindDevice(d.hwid)}
                        >
                          {t('userDetail.unbind')}
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}

          <Divider label={t('userDetail.traffic')} />
          <SegmentedControl fullWidth value={range} onChange={setRange} data={ranges()} />
          {chart.length === 0 ? (
            <p className="py-3 text-center text-ink-muted">{t('stats.noData')}</p>
          ) : (
            <>
              <TrafficArea data={chart} height={200} fmt={fmtBytes} />
              <NodeTrafficSplit
                userId={user.id}
                from={localDay(Number(range) - 1)}
                to={localDay(0)}
              />
            </>
          )}
        </div>
      )}
    </Modal>
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
