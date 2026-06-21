import { useEffect, useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import {
  deleteUser,
  getStatsSeries,
  getUserConnections,
  renameUser,
  resetUserTraffic,
  setResetPeriod,
  setUserEnabled,
  setUserLimits,
  type Connection,
  type DailyPoint,
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
  QUOTA_OPTIONS,
  RANGES,
  RESET_PERIODS,
  statusInfo,
} from './format'
import { useAction } from './hooks'
import { errMessage, notifyError } from './notify'
import { TrafficArea } from './charts'
import {
  Badge,
  Button,
  Code,
  DatePicker,
  Divider,
  Drawer,
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

function unixToDate(unix: number): string {
  return unix ? new Date(unix * 1000).toISOString().slice(0, 10) : ''
}

function LinkRow({ name, url }: { name: string; url: string }) {
  const { copied, copy } = useCopy()
  return (
    <div className="flex items-center gap-2">
      <span className="w-28 shrink-0 text-xs font-medium leading-tight">{name}</span>
      <Code className="block min-w-0 flex-1 overflow-x-auto whitespace-nowrap">{url}</Code>
      <IconButton
        color={copied ? 'teal' : 'brand'}
        onClick={() => copy(url)}
        title={copied ? 'Скопировано' : 'Копировать'}
      >
        {copied ? <IconCheck /> : <IconCopy />}
      </IconButton>
    </div>
  )
}

// EditableName renders the user's name with a pencil; clicking it swaps to an
// inline input with save/cancel. Used as the drawer title.
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
          className="shrink-0 text-gray-400 transition hover:text-brand-600"
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
        className="shrink-0 text-teal-600 transition hover:text-teal-700 disabled:opacity-50"
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
}: {
  user: User | null
  onClose: () => void
  onChanged: () => void
}) {
  const [series, setSeries] = useState<DailyPoint[]>([])
  const [conns, setConns] = useState<Connection[]>([])
  const [range, setRange] = useState('30')
  const [limitGb, setLimitGb] = useState('0')
  const sub = useCopy()
  const email = useCopy()
  const { confirm, confirmNode } = useConfirm()

  useEffect(() => {
    setLimitGb(user && user.data_limit ? String(user.data_limit / (1024 * 1024 * 1024)) : '0')
  }, [user])

  useEffect(() => {
    if (!user) {
      setSeries([])
      return
    }
    let alive = true // guard against an out-of-order response after a user switch
    const from = range === 'all' ? '2000-01-01' : localDay(Number(range) - 1)
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
    getUserConnections(user.id)
      .then((d) => alive && setConns(d))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user])

  const chart = series.map((p) => ({ day: p.day.slice(5), up: p.up, down: p.down }))
  const fail = (e: unknown) => notifyError(errMessage(e))

  const links: Array<[string, string]> = user
    ? [
        ['VLESS-TCP-TLS', user.vless],
        ['VLESS-GRPC-REALITY', user.reality],
        ['TROJAN-WS', user.trojan],
        ['HYSTERIA-UDP', user.hysteria2],
      ]
    : []

  const quotaData = user
    ? QUOTA_OPTIONS.some((o) => o.value === limitGb)
      ? QUOTA_OPTIONS
      : [...QUOTA_OPTIONS, { value: limitGb, label: fmtBytes(user.data_limit) }]
    : QUOTA_OPTIONS

  return (
    <>
    <Drawer
      open={!!user}
      onClose={onClose}
      side="right"
      title={user ? <EditableName user={user} onChanged={onChanged} /> : ''}
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
              onChange={(v) => setUserEnabled(user.id, v).then(onChanged).catch(fail)}
            />
          </div>

          <DatePicker
            label="Действует до"
            value={unixToDate(user.expire_at)}
            onChange={(v) => {
              const ea = v ? Math.floor(new Date(v).getTime() / 1000) : 0
              setUserLimits(user.id, user.data_limit, ea).then(onChanged).catch(fail)
            }}
          />

          <Select
            label="Лимит трафика"
            data={quotaData}
            value={limitGb}
            onChange={(v) => {
              setLimitGb(v)
              setUserLimits(user.id, gbToBytes(Number(v)), user.expire_at).then(onChanged).catch(fail)
            }}
          />
          <Select
            label="Автосброс трафика"
            data={RESET_PERIODS}
            value={user.reset_period || 'none'}
            onChange={(v) => setResetPeriod(user.id, v).then(onChanged).catch(fail)}
          />
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
            <div className="rounded-lg bg-white p-3">
              <QRCodeSVG value={user.sub_url} size={200} />
            </div>
          </div>
          <Code block>{user.sub_url}</Code>
          <div className="flex gap-2">
            <Button size="xs" color={sub.copied ? 'teal' : 'brand'} onClick={() => sub.copy(user.sub_url)}>
              {sub.copied ? 'Скопировано' : 'Копировать подписку'}
            </Button>
            <Button size="xs" variant="light" href={user.sub_url} target="_blank">
              Открыть подписку
            </Button>
          </div>

          <Divider label="Ссылки подключения" />
          <div className="flex flex-col gap-2">
            {links
              .filter(([, url]) => url)
              .map(([name, url]) => (
                <LinkRow key={name} name={name} url={url} />
              ))}
          </div>

          <Divider label="Трафик" />
          <SegmentedControl fullWidth value={range} onChange={setRange} data={RANGES} />
          {chart.length === 0 ? (
            <p className="py-3 text-center text-ink-muted">Нет данных</p>
          ) : (
            <TrafficArea data={chart} height={200} fmt={fmtBytes} />
          )}

          <Divider label="Подключения (IP)" />
          {conns.length === 0 ? (
            <p className="py-2 text-center text-sm text-ink-muted">Нет данных о подключениях</p>
          ) : (
            <div className="flex flex-col gap-1">
              {conns.map((c) => (
                <div key={c.ip} className="flex items-center justify-between gap-2">
                  <span className="font-mono text-sm">{c.ip}</span>
                  <span className="text-xs text-ink-muted">
                    {fmtLastSeen(c.last_seen)} · {c.count}×
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </Drawer>
    {confirmNode}
    </>
  )
}
