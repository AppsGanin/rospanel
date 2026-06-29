import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  type BulkAction,
  bulkUsers,
  createUser,
  listUsers,
  setResetPeriod,
  setUserEnabled,
  type User,
} from "./api";
import { useAction } from "./hooks";
import {
  fmtExpire,
  fmtQuota,
  gbToBytes,
  isOnline,
  QUOTA_OPTIONS,
  RESET_PERIODS,
  statusInfo,
} from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  cn,
  Code,
  DatePicker,
  IconCheck,
  Modal,
  Select,
  Skeleton,
  Switch,
  TextInput,
  useCopy,
} from "./ui";
import { UserDetail } from "./UserDetail";

function UsersSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {[...Array(4)].map((_, i) => (
        <Card key={i} className="p-4">
          <div className="mb-3 flex items-center gap-2">
            <Skeleton className="h-5 w-9 rounded-full" />
            <Skeleton className="h-4 w-32" />
          </div>
          <div className="mb-3 flex gap-2">
            <Skeleton className="h-5 w-16 rounded-full" />
            <Skeleton className="h-5 w-14 rounded-full" />
            <Skeleton className="h-5 w-20 rounded-full" />
          </div>
          <div className="flex gap-2">
            <Skeleton className="h-8 flex-1 rounded-lg" />
            <Skeleton className="h-8 flex-1 rounded-lg" />
            <Skeleton className="h-8 w-8 rounded-lg" />
          </div>
        </Card>
      ))}
    </div>
  );
}

// Status filter options for the toolbar (keys match User.status).
const STATUS_FILTERS = [
  { value: "all", label: "Все статусы" },
  { value: "active", label: "Активные" },
  { value: "disabled", label: "Выключенные" },
  { value: "expired", label: "Истёкшие" },
  { value: "limited", label: "Лимит трафика" },
  { value: "device_limited", label: "Лимит устройств" },
];

const SORTS = [
  { value: "new", label: "Сначала новые" },
  { value: "name", label: "По имени" },
  { value: "traffic", label: "По трафику" },
  { value: "expiry", label: "По сроку" },
  { value: "online", label: "По онлайну" },
];

const EXTEND_PRESETS = [7, 30, 90, 180];
const PAGE_SIZE = 100;

// expSortKey orders by soonest expiry; "never" (0) sorts last.
const expSortKey = (u: User) => (u.expire_at > 0 ? u.expire_at : Infinity);

export function UsersPanel() {
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  const [detail, setDetail] = useState<User | null>(null);
  const [loaded, setLoaded] = useState(false);

  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [sort, setSort] = useState("new");
  const [page, setPage] = useState(1);

  const [selected, setSelected] = useState<Set<number>>(new Set());
  // pending is the bulk action currently in flight (null = none). Tracking the
  // specific action lets only the clicked button show a spinner, and keeps the
  // action bar from reflowing/jumping while one runs.
  const [pending, setPending] = useState<BulkAction | null>(null);
  const [extendOpen, setExtendOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const refresh = useCallback(() => {
    listUsers()
      .then((us) => {
        setUsers(us);
        setDetail((d) => (d ? (us.find((x) => x.id === d.id) ?? d) : d));
        // Drop any selection that refers to users no longer present.
        setSelected((prev) => {
          const live = new Set(us.map((u) => u.id));
          const kept = [...prev].filter((id) => live.has(id));
          return kept.length === prev.size ? prev : new Set(kept);
        });
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Filtering / sorting / paging are client-side: the full list is already loaded
  // and stays snappy well into the hundreds, so this avoids any API round-trips.
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return users.filter(
      (u) =>
        (statusFilter === "all" || u.status === statusFilter) &&
        (q === "" ||
          u.name.toLowerCase().includes(q) ||
          String(u.id) === q),
    );
  }, [users, query, statusFilter]);

  const sorted = useMemo(() => {
    const arr = [...filtered];
    switch (sort) {
      case "name":
        arr.sort((a, b) => a.name.localeCompare(b.name, "ru"));
        break;
      case "traffic":
        arr.sort(
          (a, b) => b.used_up + b.used_down - (a.used_up + a.used_down),
        );
        break;
      case "expiry":
        arr.sort((a, b) => expSortKey(a) - expSortKey(b));
        break;
      case "online":
        arr.sort((a, b) => b.last_seen - a.last_seen);
        break;
      default:
        arr.sort((a, b) => b.id - a.id); // newest first
    }
    return arr;
  }, [filtered, sort]);

  const pageCount = Math.max(1, Math.ceil(sorted.length / PAGE_SIZE));
  const curPage = Math.min(page, pageCount);
  const paged = sorted.slice((curPage - 1) * PAGE_SIZE, curPage * PAGE_SIZE);

  // Reset to the first page whenever the result set changes shape.
  useEffect(() => {
    setPage(1);
  }, [query, statusFilter, sort]);

  const filteredIds = useMemo(() => filtered.map((u) => u.id), [filtered]);
  const allFilteredSelected =
    filteredIds.length > 0 && filteredIds.every((id) => selected.has(id));

  const toggleOne = (id: number, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const toggleAllFiltered = () =>
    setSelected((prev) => {
      if (allFilteredSelected) {
        const next = new Set(prev);
        filteredIds.forEach((id) => next.delete(id));
        return next;
      }
      return new Set([...prev, ...filteredIds]);
    });

  const clearSelection = () => setSelected(new Set());

  const runBulk = async (action: BulkAction, days = 0) => {
    const ids = [...selected];
    if (ids.length === 0) return;
    setPending(action);
    try {
      const { affected } = await bulkUsers(ids, action, days);
      notifySuccess(`Готово — затронуто пользователей: ${affected}`);
      clearSelection();
      setExtendOpen(false);
      setConfirmDelete(false);
      refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setPending(null);
    }
  };

  // renderBulkActions builds the five action buttons shared by both bar layouts.
  // grid=true → full-width cells for the mobile 2-col grid; grid=false → inline
  // shrink-0 buttons for the desktop row. Only the in-flight action spins.
  const renderBulkActions = (grid: boolean) => {
    const cls = grid ? "" : "shrink-0";
    return [
      <Button key="enable" size="sm" variant="light" fullWidth={grid} className={cls} loading={pending === "enable"} disabled={pending !== null} onClick={() => runBulk("enable")}>
        Включить
      </Button>,
      <Button key="disable" size="sm" variant="light" color="gray" fullWidth={grid} className={cls} loading={pending === "disable"} disabled={pending !== null} onClick={() => runBulk("disable")}>
        Выключить
      </Button>,
      <Button key="reset" size="sm" variant="light" color="gray" fullWidth={grid} className={cls} loading={pending === "reset"} disabled={pending !== null} onClick={() => runBulk("reset")}>
        Сбросить трафик
      </Button>,
      <Button key="extend" size="sm" variant="light" fullWidth={grid} className={cls} disabled={pending !== null} onClick={() => setExtendOpen(true)}>
        Продлить
      </Button>,
      <Button key="delete" size="sm" variant="light" color="red" fullWidth={grid} className={grid ? "col-span-2" : "shrink-0"} disabled={pending !== null} onClick={() => setConfirmDelete(true)}>
        Удалить
      </Button>,
    ];
  };

  if (!loaded) return <UsersSkeleton />;

  if (users.length === 0) {
    return (
      <>
        <p className="py-12 text-center text-ink-muted">
          Пока нет пользователей. Нажмите кнопку «+» внизу справа.
        </p>
        <AddFab onClick={() => setAddOpen(true)} />
        <AddUser
          opened={addOpen}
          onClose={() => {
            setAddOpen(false);
            refresh();
          }}
        />
      </>
    );
  }

  return (
    <>
      {/* Toolbar: search grows, the two selects keep a fixed width so they don't
          crowd out the search box. */}
      <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
        <div className="min-w-0 sm:flex-1">
          <TextInput
            value={query}
            onChange={setQuery}
            placeholder="Поиск по имени или ID…"
          />
        </div>
        <div className="sm:w-48 sm:shrink-0">
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            data={STATUS_FILTERS}
          />
        </div>
        <div className="sm:w-48 sm:shrink-0">
          <Select value={sort} onChange={setSort} data={SORTS} />
        </div>
      </div>

      <div className="mb-3 flex items-center justify-between gap-3 text-sm text-ink-muted">
        <span>
          {filtered.length === users.length
            ? `Всего: ${users.length}`
            : `Найдено: ${filtered.length} из ${users.length}`}
        </span>
        {filtered.length > 0 && (
          <button
            onClick={toggleAllFiltered}
            className="font-medium text-accent hover:underline"
          >
            {allFilteredSelected
              ? "Снять выделение"
              : `Выбрать все (${filtered.length})`}
          </button>
        )}
      </div>

      {filtered.length === 0 ? (
        <p className="py-12 text-center text-ink-muted">
          Ничего не найдено. Измените поиск или фильтр.
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {paged.map((u) => {
            const st = statusInfo(u.status);
            const checked = selected.has(u.id);
            return (
              <Card
                key={u.id}
                className={`p-4 ${checked ? "ring-2 ring-accent" : ""}`}
              >
                <div className="mb-3 flex min-w-0 items-center gap-2">
                  <SelectCheck
                    checked={checked}
                    onChange={(v) => toggleOne(u.id, v)}
                    label={`Выбрать ${u.name}`}
                  />
                  <Switch
                    checked={u.enabled}
                    onChange={(v) =>
                      setUserEnabled(u.id, v)
                        .then(refresh)
                        .catch((e) => notifyError(errMessage(e)))
                    }
                  />
                  <span
                    className={`flex-1 truncate font-medium ${
                      u.status === "active" ? "text-ink" : "text-ink-muted"
                    }`}
                  >
                    {u.name}
                  </span>
                </div>

                <div className="mb-3 flex flex-wrap gap-2">
                  <Badge color={st.color as never}>{st.label}</Badge>
                  <Badge color={isOnline(u.last_seen) ? "greenSolid" : "gray"}>
                    {isOnline(u.last_seen) ? "● онлайн" : "офлайн"}
                  </Badge>
                  <Badge color="brand">
                    {fmtQuota(u.used_up + u.used_down, u.data_limit)}
                  </Badge>
                  {u.expire_at > 0 && (
                    <Badge color="gray">до {fmtExpire(u.expire_at)}</Badge>
                  )}
                  {u.device_limit > 0 && (
                    <Badge color={u.status === "device_limited" ? "orange" : "gray"}>
                      {u.active_devices}/{u.device_limit} устр.
                    </Badge>
                  )}
                </div>

                <div className="flex gap-2 mt-auto">
                  <Button
                    size="sm"
                    variant="light"
                    href={u.sub_url}
                    target="_blank"
                    className="flex-1"
                  >
                    Подписка
                  </Button>
                  <Button
                    size="sm"
                    variant="light"
                    color="gray"
                    onClick={() => setDetail(u)}
                    className="flex-1"
                  >
                    Подробнее
                  </Button>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      {pageCount > 1 && (
        <div className="mt-4 flex items-center justify-center gap-3">
          <Button
            size="sm"
            variant="light"
            color="gray"
            disabled={curPage <= 1}
            onClick={() => setPage(curPage - 1)}
          >
            Назад
          </Button>
          <span className="text-sm text-ink-muted">
            {curPage} / {pageCount}
          </span>
          <Button
            size="sm"
            variant="light"
            color="gray"
            disabled={curPage >= pageCount}
            onClick={() => setPage(curPage + 1)}
          >
            Вперёд
          </Button>
        </div>
      )}

      {/* The add-user FAB hides while a bulk selection is active (the bulk bar
          takes the bottom slot). */}
      {selected.size === 0 && <AddFab onClick={() => setAddOpen(true)} />}

      {/* Reserve scroll space so the last cards aren't hidden behind the fixed
          selection bar (taller on mobile, where it stacks into a grid). */}
      {selected.size > 0 && <div aria-hidden className="h-44 sm:h-20" />}

      {selected.size > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-40 border-t border-gray-200 bg-white/95 px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] shadow-lg backdrop-blur">
          <div className="mx-auto max-w-3xl">
            {/* Mobile: count + cancel on top, actions in a 2-col grid below — no
                horizontal scroll, and a fixed grid means the height can't jump
                when a button shows its spinner. */}
            <div className="sm:hidden">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-sm font-medium text-ink">
                  Выбрано: {selected.size}
                </span>
                <button
                  onClick={clearSelection}
                  disabled={pending !== null}
                  className="text-sm font-medium text-ink-muted hover:text-ink disabled:opacity-60"
                >
                  Отмена
                </button>
              </div>
              <div className="grid grid-cols-2 gap-2">{renderBulkActions(true)}</div>
            </div>

            {/* Desktop: single row; actions scroll horizontally only if they don't
                fit, the label and "Отмена" stay pinned. */}
            <div className="hidden items-center gap-2 sm:flex">
              <span className="shrink-0 text-sm font-medium text-ink">
                Выбрано: {selected.size}
              </span>
              <div className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto py-0.5">
                {renderBulkActions(false)}
              </div>
              <Button
                size="sm"
                variant="subtle"
                color="gray"
                className="shrink-0"
                disabled={pending !== null}
                onClick={clearSelection}
              >
                Отмена
              </Button>
            </div>
          </div>
        </div>
      )}

      <ExtendModal
        open={extendOpen}
        count={selected.size}
        busy={pending === "extend"}
        onApply={(days) => runBulk("extend", days)}
        onClose={() => setExtendOpen(false)}
      />

      <Modal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title="Удалить пользователей?"
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm text-ink-muted">
            Будет удалено пользователей: {selected.size}. Действие необратимо —
            их подписки и ссылки сразу перестанут работать.
          </p>
          <div className="flex gap-2">
            <Button
              color="red"
              fullWidth
              loading={pending === "delete"}
              onClick={() => runBulk("delete")}
            >
              Удалить {selected.size}
            </Button>
            <Button
              variant="subtle"
              color="gray"
              fullWidth
              onClick={() => setConfirmDelete(false)}
            >
              Отмена
            </Button>
          </div>
        </div>
      </Modal>

      <AddUser
        opened={addOpen}
        onClose={() => {
          setAddOpen(false);
          refresh();
        }}
      />

      <UserDetail
        user={detail}
        onChanged={refresh}
        onClose={() => {
          setDetail(null);
          refresh();
        }}
      />
    </>
  );
}

// AddFab is the floating "+" button to add a user.
function AddFab({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      aria-label="Добавить пользователя"
      title="Добавить пользователя"
      className="fixed bottom-6 right-6 z-40 flex h-14 w-14 items-center justify-center rounded-full bg-brand-600 text-onaccent shadow-lg transition hover:bg-brand-700 hover:shadow-xl active:scale-95"
    >
      <svg
        width="26"
        height="26"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
      >
        <path d="M12 5v14M5 12h14" />
      </svg>
    </button>
  );
}

// SelectCheck is a compact, theme-aware selection checkbox: a real (screen-reader
// visible) input drives a custom box drawn with the same tokens as the rest of the
// UI, so it follows the light/dark theme instead of the browser's white default.
function SelectCheck({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
}) {
  return (
    <label className="flex shrink-0 cursor-pointer items-center" title="Выбрать">
      <input
        type="checkbox"
        className="sr-only"
        checked={checked}
        aria-label={label}
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
      <span
        className={cn(
          "flex h-6 w-6 items-center justify-center rounded-md border transition",
          checked
            ? "border-brand-600 bg-brand-600 text-onaccent"
            : "border-gray-300 bg-white hover:border-gray-400",
        )}
      >
        {checked && <IconCheck size={14} />}
      </span>
    </label>
  );
}

// ExtendModal asks how many days to add to the selected users' expiry. Users with
// no expiry are skipped server-side (extending "never" is meaningless).
function ExtendModal({
  open,
  count,
  busy,
  onApply,
  onClose,
}: {
  open: boolean;
  count: number;
  busy: boolean;
  onApply: (days: number) => void;
  onClose: () => void;
}) {
  const [days, setDays] = useState("30");
  const n = Math.floor(Number(days) || 0);
  return (
    <Modal open={open} onClose={onClose} title="Продлить подписку">
      <div className="flex flex-col gap-4">
        <p className="text-sm text-ink-muted">
          Срок будет продлён у выбранных пользователей ({count}). Те, у кого срок
          не задан (бессрочные), не меняются.
        </p>
        <div className="flex flex-wrap gap-2">
          {EXTEND_PRESETS.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={n === p ? "filled" : "light"}
              color="gray"
              onClick={() => setDays(String(p))}
            >
              +{p} дн.
            </Button>
          ))}
        </div>
        <TextInput
          label="Дней"
          type="number"
          value={days}
          onChange={setDays}
        />
        <div className="flex gap-2">
          <Button
            fullWidth
            loading={busy}
            disabled={n <= 0}
            onClick={() => onApply(n)}
          >
            Продлить на {n > 0 ? n : "—"} дн.
          </Button>
          <Button variant="subtle" color="gray" fullWidth onClick={onClose}>
            Отмена
          </Button>
        </div>
      </div>
    </Modal>
  );
}

function AddUser({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [limitGb, setLimitGb] = useState("0");
  const [resetPeriod, setResetPeriodState] = useState("none");
  const [expDate, setExpDate] = useState("");
  const [created, setCreated] = useState<User | null>(null);
  const { busy, run } = useAction();
  const { copied, copy } = useCopy();

  const submit = async () => {
    if (!name.trim()) return;
    run(async () => {
      const dl = gbToBytes(Number(limitGb) || 0);
      const ea = expDate ? Math.floor(new Date(expDate).getTime() / 1000) : 0;
      const u = await createUser(name.trim(), dl, ea);
      if (resetPeriod !== "none") await setResetPeriod(u.id, resetPeriod);
      setCreated(u);
    });
  };

  const close = () => {
    setName("");
    setLimitGb("0");
    setResetPeriodState("none");
    setExpDate("");
    setCreated(null);
    onClose();
  };

  return (
    <Modal
      open={opened}
      onClose={close}
      title={created ? "Пользователь создан" : "Новый пользователь"}
    >
      {!created ? (
        <div className="flex flex-col gap-3">
          <TextInput
            label="Имя"
            placeholder="например, Андрей"
            value={name}
            onChange={setName}
            autoFocus
          />
          <div className="grid grid-cols-2 gap-3">
            <DatePicker
              label="Действует до"
              value={expDate}
              onChange={setExpDate}
              min={new Date().toISOString().slice(0, 10)}
            />
            <Select
              label="Лимит трафика"
              data={QUOTA_OPTIONS}
              value={limitGb}
              onChange={setLimitGb}
            />
          </div>
          <Select
            label="Автосброс трафика"
            data={RESET_PERIODS}
            value={resetPeriod}
            onChange={setResetPeriodState}
          />
          <Button loading={busy} onClick={submit}>
            Создать и показать ссылку
          </Button>
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3">
          <p className="text-sm text-ink-muted">
            Подписка (все протоколы) — отсканируйте или поделитесь ссылкой
          </p>
          <div className="rounded-lg bg-onaccent p-3">
            <QRCodeSVG value={created.sub_url} size={200} />
          </div>
          <Code block className="w-full">
            {created.sub_url}
          </Code>
          <Button
            fullWidth
            color={copied ? "teal" : "brand"}
            onClick={() => copy(created.sub_url)}
          >
            {copied ? "Скопировано" : "Скопировать ссылку подписки"}
          </Button>
          <Button
            variant="light"
            fullWidth
            href={created.sub_url}
            target="_blank"
          >
            Открыть подписку
          </Button>
          <Button variant="subtle" color="gray" fullWidth onClick={close}>
            Готово
          </Button>
        </div>
      )}
    </Modal>
  );
}
