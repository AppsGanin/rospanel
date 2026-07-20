import { useEffect, useRef, useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import {
  deleteUser,
  genUserTelegramLink,
  getBilling,
  getStatsSeries,
  getUserConnections,
  renameUser,
  resetUserTraffic,
  rotateSubToken,
  messageUser,
  unlinkUserTelegram,
  setResetPeriod,
  setUserEnabled,
  setUserLimits,
  setUserPlan,
  type Connection,
  type DailyPoint,
  type TariffPlan,
  type User,
} from './api'
import {
  fmtBytes,
  fmtExpire,
  fmtLastSeen,
  fmtQuota,
  gbToBytes,
  isOnline,
  localDay,
  DEVICE_LIMIT_OPTIONS,
  QUOTA_OPTIONS,
  RANGES,
  RESET_PERIODS,
  statusInfo,
} from './format'
import { useAction } from './hooks'
import { HtmlEditor } from './HtmlEditor'
import { errMessage, notifyError, notifySuccess } from './notify'
import { TrafficArea } from './charts'
import { UserEventsModal } from './UserEventsModal'
import {
  Badge,
  Button,
  Code,
  DatePicker,
  Divider,
  Modal,
  IconButton,
  IconCheck,
  IconClose,
  IconCopy,
  IconPencil,
  SegmentedControl,
  Select,
  Switch,
  useConfirm,
  useCopy,
} from './ui'

// planSelectData builds the tariff dropdown: "manual" plus enabled plans, and a
// fallback entry if the user is on a plan that's hidden/disabled (so the current
// value still resolves to a label).
function planSelectData(plans: TariffPlan[], user: User) {
  const data = [
    { value: '0', label: 'Вручную (без лимитов)' },
    ...plans
      .filter((p) => p.enabled)
      .map((p) => ({
        value: String(p.id),
        label: p.name,
      })),
  ]
  if (user.plan_id && !data.some((o) => o.value === String(user.plan_id))) {
    data.push({ value: String(user.plan_id), label: user.plan_name || `тариф #${user.plan_id}` })
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

// dateLabel renders an expiry (unix or a "YYYY-MM-DD" picker value) for the
// confirmation text.
function dateLabel(v: number | string): string {
  if (!v) return 'бессрочно'
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v)
  return d.toLocaleDateString('ru-RU')
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
          title="Переименовать"
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
        title="Сохранить"
        className="shrink-0 text-success transition hover:text-success disabled:opacity-50"
      >
        <IconCheck size={18} />
      </button>
      <button
        onClick={() => setEditing(false)}
        title="Отмена"
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
  const [series, setSeries] = useState<DailyPoint[]>([])
  const [conns, setConns] = useState<Connection[]>([])
  const [range, setRange] = useState('30')
  const [limitGb, setLimitGb] = useState('0')
  const [deviceLimit, setDeviceLimit] = useState('0')
  const [billingOn, setBillingOn] = useState(false)
  const [plans, setPlans] = useState<TariffPlan[]>([])
  const [tgLink, setTgLink] = useState<{ url: string; mins: number } | null>(null)
  const [eventsOpen, setEventsOpen] = useState(false)
  const [msgOpen, setMsgOpen] = useState(false)
  const [msgText, setMsgText] = useState('')
  const [msgMedia, setMsgMedia] = useState<File | null>(null)
  const msgFileRef = useRef<HTMLInputElement>(null)
  const [sending, setSending] = useState(false)
  const email = useCopy()
  const { confirm, confirmNode } = useConfirm()

  useEffect(() => {
    setLimitGb(user && user.data_limit ? String(user.data_limit / (1024 * 1024 * 1024)) : '0')
    setDeviceLimit(user ? String(user.device_limit ?? 0) : '0')
    setTgLink(null) // a one-time bind link is per-user; don't leak it across switches
    setEventsOpen(false) // ditto for the journal — never show one user's trail over another
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

  const quotaData = user
    ? QUOTA_OPTIONS.some((o) => o.value === limitGb)
      ? QUOTA_OPTIONS
      : [...QUOTA_OPTIONS, { value: limitGb, label: fmtBytes(user.data_limit) }]
    : QUOTA_OPTIONS

  const saveLimits = (dl: number, ea: number, dev: number) =>
    setUserLimits(user!.id, dl, ea, dev).then(onChanged).catch(fail)

  // confirmChange gates an edit in the "Управление" block. These controls apply
  // to a live subscription the moment they're touched, so a misclick would
  // otherwise silently change what the user is paying for.
  const confirmChange = async (field: string, from: string, to: string, apply: () => void) => {
    const ok = await confirm({
      title: 'Изменить настройку?',
      body: `«${field}» для «${user!.name}»: ${from} → ${to}.`,
      confirmLabel: 'Изменить',
    })
    if (ok) apply()
  }

  const activeConnCount = user ? conns.filter((c) => isOnline(c.last_seen)).length : 0

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
              {isOnline(user.last_seen) ? '● онлайн' : `офлайн · ${fmtLastSeen(user.last_seen)}`}
            </Badge>
            <Badge color="brand">{fmtQuota(user.used_up + user.used_down, user.data_limit)}</Badge>
            {user.expire_at > 0 && (
              <Badge color="gray">до {fmtExpire(user.expire_at)}</Badge>
            )}
            {user.device_limit > 0 && (
              <Badge color={user.status === 'device_limited' ? 'orange' : 'gray'}>
                устройств {user.active_devices}/{user.device_limit}
              </Badge>
            )}
          </div>

          <div className="flex items-center gap-2 text-sm text-ink-muted">
            <span className="shrink-0">ID в системе:</span>
            <Code>{user.system_email}</Code>
            <button
              onClick={() => email.copy(user.system_email)}
              className="text-gray-400 transition hover:text-gray-600"
              title="Скопировать"
            >
              {email.copied ? <IconCheck /> : <IconCopy />}
            </button>
          </div>

          <Divider label="Управление" />
          <div className="flex items-center justify-between">
            <span className="text-sm">{user.enabled ? 'Подписка включена' : 'Подписка выключена'}</span>
            <Switch
              checked={user.enabled}
              onChange={(v) =>
                confirmChange(
                  'Подписка',
                  user.enabled ? 'включена' : 'выключена',
                  v ? 'включена' : 'выключена',
                  () => setUserEnabled(user.id, v).then(onChanged).catch(fail),
                )
              }
            />
          </div>

          <DatePicker
            label="Действует до"
            value={unixToDate(user.expire_at)}
            onChange={(v) => {
              const ea = v ? Math.floor(new Date(v).getTime() / 1000) : 0
              confirmChange('Действует до', dateLabel(user.expire_at), dateLabel(v), () =>
                saveLimits(user.data_limit, ea, user.device_limit),
              )
            }}
          />

          <Select
            label="Лимит трафика"
            data={quotaData}
            value={limitGb}
            onChange={(v) =>
              confirmChange(
                'Лимит трафика',
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
            label="Лимит устройств"
            data={DEVICE_LIMIT_OPTIONS}
            value={deviceLimit}
            onChange={(v) =>
              confirmChange(
                'Лимит устройств',
                optLabel(DEVICE_LIMIT_OPTIONS, deviceLimit),
                optLabel(DEVICE_LIMIT_OPTIONS, v),
                () => {
                  setDeviceLimit(v)
                  saveLimits(user.data_limit, user.expire_at, Number(v))
                },
              )
            }
          />
          <p className="-mt-1 text-xs text-ink-muted">
            Одно устройство = один публичный IP. Телефон и компьютер в одной Wi‑Fi сети
            считаются одним устройством. Для раздельного учёта используйте мобильный
            интернет на одном из них.
          </p>
          <Select
            label="Автосброс трафика"
            data={RESET_PERIODS}
            value={user.reset_period || 'none'}
            onChange={(v) =>
              confirmChange(
                'Автосброс трафика',
                optLabel(RESET_PERIODS, user.reset_period || 'none'),
                optLabel(RESET_PERIODS, v),
                () => setResetPeriod(user.id, v).then(onChanged).catch(fail),
              )
            }
          />
          {billingOn && (
            <>
              <Select
                label="Тариф"
                data={planSelectData(plans, user)}
                value={String(user.plan_id || 0)}
                onChange={(v) =>
                  confirmChange(
                    'Тариф',
                    optLabel(planSelectData(plans, user), String(user.plan_id || 0)),
                    optLabel(planSelectData(plans, user), v),
                    () => setUserPlan(user.id, Number(v)).then(onChanged).catch(fail),
                  )
                }
              />
              <p className="-mt-1 text-xs text-ink-muted">
                Назначение тарифа применяет его лимиты трафика, срок и устройства.
                «Вручную» снимает тариф и обнуляет лимиты.
              </p>
            </>
          )}
          <Button variant="light" onClick={() => setEventsOpen(true)}>
            Журнал действий
          </Button>
          <Button
            color="orange"
            variant="light"
            onClick={async () => {
              const ok = await confirm({
                title: 'Сбросить трафик?',
                body: `Счётчик трафика пользователя «${user.name}» будет обнулён.`,
                confirmLabel: 'Сбросить',
                danger: true,
              })
              if (ok) resetUserTraffic(user.id).then(onChanged).catch(fail)
            }}
          >
            Сбросить трафик
          </Button>
          <Button
            color="red"
            variant="light"
            onClick={async () => {
              const ok = await confirm({
                title: 'Удалить пользователя?',
                body: `Пользователь «${user.name}» будет удалён. Это действие необратимо.`,
                confirmLabel: 'Удалить',
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
            Удалить пользователя
          </Button>

          <Divider label="Подписка" />
          <div className="flex justify-center">
            <div className="rounded-lg bg-onaccent p-3">
              <QRCodeSVG value={user.sub_url} size={200} />
            </div>
          </div>
          <Code block copy>{user.sub_url}</Code>
          <div className="flex flex-wrap gap-2">
            <Button size="xs" variant="light" href={user.sub_url} target="_blank">
              Открыть подписку
            </Button>
            <Button
              size="xs"
              variant="light"
              color="orange"
              onClick={async () => {
                const ok = await confirm({
                  title: 'Сбросить ссылку подписки?',
                  body:
                    'Будет выдана новая ссылка. Старая перестанет работать — на всех устройствах нужно обновить подписку в клиенте. UUID и пароли протоколов не меняются.',
                  confirmLabel: 'Сбросить ссылку',
                  danger: true,
                })
                if (!ok) return
                rotateSubToken(user.id)
                  .then(() => {
                    notifySuccess('Ссылка подписки обновлена')
                    onChanged()
                  })
                  .catch(fail)
              }}
            >
              Сбросить ссылку
            </Button>
          </div>

          <Divider label="Telegram" />
          {user.telegram_linked ? (
            <div className="flex flex-col gap-2">
              <p className="text-sm text-success">Бот привязан к чату пользователя</p>
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
                    Отправить сообщение
                  </Button>
              )}
              <Button
                size="xs"
                variant="light"
                color="orange"
                onClick={async () => {
                  const ok = await confirm({
                    title: 'Отвязать Telegram?',
                    body: 'Пользователь потеряет доступ к боту, пока снова не откроет ссылку привязки.',
                    confirmLabel: 'Отвязать',
                    danger: true,
                  })
                  if (ok) unlinkUserTelegram(user.id).then(onChanged).catch(fail)
                }}
              >
                Отвязать Telegram
              </Button>
            </div>
          ) : user.telegram_link ? (
            <div className="flex flex-col gap-2">
              <p className="text-sm text-ink-muted">
                Сгенерируйте одноразовую ссылку привязки и отправьте её
                пользователю — он откроет её и привяжет аккаунт к боту.
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
                Получить ссылку привязки
              </Button>
              {tgLink && (
                <>
                  <Code block copy>{tgLink.url}</Code>
                  <p className="text-xs text-ink-muted">
                    Скопируйте и отправьте пользователю. Одноразовая ссылка,
                    действует {tgLink.mins} мин.
                  </p>
                </>
              )}
            </div>
          ) : (
            <p className="text-sm text-ink-muted">
              Включите пользовательского бота в настройках Telegram.
            </p>
          )}

          <Divider label="Устройства (IP)" />
          <p className="text-sm text-ink-muted">
            {user.device_limit > 0
              ? `Активно ${activeConnCount} из ${user.device_limit} · всего ${conns.length} IP`
              : `Активно ${activeConnCount} · всего ${conns.length} IP`}
          </p>
          {conns.length === 0 ? (
            <p className="py-2 text-center text-sm text-ink-muted">Пока нет подключений</p>
          ) : (
            <div className="flex flex-col gap-1.5">
              {conns.map((c) => (
                <div
                  key={c.ip}
                  className="flex items-center justify-between gap-2 rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    {isOnline(c.last_seen) ? (
                      <Badge color="greenSolid">онлайн</Badge>
                    ) : (
                      <Badge color="gray">офлайн</Badge>
                    )}
                    <span className="truncate font-mono text-sm">{c.ip}</span>
                  </div>
                  <span className="shrink-0 text-xs text-ink-muted">
                    {fmtLastSeen(c.last_seen)} · {c.count}×
                  </span>
                </div>
              ))}
            </div>
          )}

          <Divider label="Трафик" />
          <SegmentedControl fullWidth value={range} onChange={setRange} data={RANGES} />
          {chart.length === 0 ? (
            <p className="py-3 text-center text-ink-muted">Нет данных</p>
          ) : (
            <TrafficArea data={chart} height={200} fmt={fmtBytes} />
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
        title={`Сообщение для ${user.name}`}
      >
        <HtmlEditor
          value={msgText}
          onChange={setMsgText}
          rows={5}
          placeholder="Текст придёт в чат с ботом."
        />
        <p className="mt-1 text-xs text-ink-muted">
          {msgMedia
            ? `${[...msgText].length} / 1024 — с вложением Telegram ограничивает подпись`
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
                Убрать
              </Button>
            </div>
          ) : (
            <Button
              variant="light"
              size="sm"
              onClick={() => msgFileRef.current?.click()}
            >
              Прикрепить файл
            </Button>
          )}
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={() => setMsgOpen(false)}>
            Отмена
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
                notifySuccess('Сообщение отправлено')
              } catch (e) {
                notifyError(errMessage(e))
              } finally {
                setSending(false)
              }
            }}
          >
            Отправить
          </Button>
        </div>
      </Modal>
    )}
    {confirmNode}
    </>
  )
}
