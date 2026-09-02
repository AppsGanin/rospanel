import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type BulkAction,
  bulkUsers,
  createUser,
  exportUsersURL,
  listUsers,
  setResetPeriod,
  setUserEnabled,
  type User,
} from "./api";
import { useAction, useViewMode } from "./hooks";
import i18n, { currentLang } from "./i18n";
import {
  fmtExpire,
  fmtQuota,
  gbToBytes,
  isOnline,
  quotaOptions,
  resetPeriods,
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
  IconButton,
  IconExport,
  IconExternal,
  IconImport,
  IconEye,
  Select,
  Skeleton,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
  TableShell,
  THead,
  TR,
  ViewSwitch,
  TD,
} from "./ui";
import { UserDetail } from "./UserDetail";
import { ImportUsersModal } from "./ImportUsersModal";

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
const statusFilters = () => [
  { value: "all", label: i18n.t("usersPanel.fAll") },
  { value: "active", label: i18n.t("usersPanel.fActive") },
  { value: "disabled", label: i18n.t("usersPanel.fDisabled") },
  { value: "expired", label: i18n.t("usersPanel.fExpired") },
  { value: "limited", label: i18n.t("usersPanel.fLimited") },
  { value: "device_limited", label: i18n.t("usersPanel.fDeviceLimited") },
];

const sorts = () => [
  { value: "new", label: i18n.t("usersPanel.sNew") },
  { value: "name", label: i18n.t("usersPanel.sName") },
  { value: "traffic", label: i18n.t("usersPanel.sTraffic") },
  { value: "expiry", label: i18n.t("usersPanel.sExpiry") },
  { value: "online", label: i18n.t("usersPanel.sOnline") },
];

const EXTEND_PRESETS = [7, 30, 90, 180];
const PAGE_SIZE = 100;

// expSortKey orders by soonest expiry; "never" (0) sorts last.
const expSortKey = (u: User) => (u.expire_at > 0 ? u.expire_at : Infinity);

export function UsersPanel({ userBotEnabled }: { userBotEnabled: boolean }) {
  const { t } = useTranslation();
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [detail, setDetail] = useState<User | null>(null);
  const [loaded, setLoaded] = useState(false);

  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  // "" = any tag. Derived from the loaded list rather than fetched: the list already
  // carries every user's tags, so the dropdown can never disagree with the rows.
  const [tagFilter, setTagFilter] = useState("");
  const [sort, setSort] = useState("new");
  const [page, setPage] = useState(1);

  // How the list is drawn. The table is the default: it is the denser view and the one
  // an operator scanning a fleet of accounts asks for. Remembered per browser, because
  // this is a working preference, not a per-visit choice.
  const [view, changeView] = useViewMode("users");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // pending is the bulk action currently in flight (null = none). Tracking the
  // specific action lets only the clicked button show a spinner, and keeps the
  // action bar from reflowing/jumping while one runs.
  const [pending, setPending] = useState<BulkAction | null>(null);
  const [extendOpen, setExtendOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { confirm, confirmNode } = useConfirm();

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
        (tagFilter === "" || (u.tags ?? []).includes(tagFilter)) &&
        (q === "" ||
          u.name.toLowerCase().includes(q) ||
          String(u.id) === q ||
          u.system_email.toLowerCase() === q ||
          (u.note ?? "").toLowerCase().includes(q) ||
          (u.tags ?? []).some((tag) => tag.includes(q))),
    );
  }, [users, query, statusFilter, tagFilter]);

  // Every tag in use with how many users carry it, most used first, for the
  // toolbar filter. A tag that disappears from every user drops out of the list,
  // and a filter pinned to it falls back to "any" in the effect below.
  const tagOptions = useMemo(() => {
    const counts = new Map<string, number>();
    for (const u of users) for (const tag of u.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1);
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([tag, count]) => ({ tag, count }));
  }, [users]);
  useEffect(() => {
    if (tagFilter && !tagOptions.some((o) => o.tag === tagFilter)) setTagFilter("");
  }, [tagFilter, tagOptions]);

  const sorted = useMemo(() => {
    const arr = [...filtered];
    switch (sort) {
      case "name":
        arr.sort((a, b) => a.name.localeCompare(b.name, currentLang()));
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
      notifySuccess(t("usersPanel.bulkDone", { count: affected }));
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

  // confirmBulk asks for confirmation before an immediate bulk action (these hit
  // many users at once and aren't trivially reversible), then runs it. Extend and
  // delete have their own dedicated dialogs.
  const confirmBulk = async (action: "enable" | "disable" | "reset") => {
    const n = selected.size;
    const opts =
      action === "enable"
        ? {
            title: t("usersPanel.enableTitle"),
            body: t("usersPanel.enableBody", { count: n }),
            confirmLabel: t("usersPanel.enable"),
          }
        : action === "disable"
          ? {
              title: t("usersPanel.disableTitle"),
              body: t("usersPanel.disableBody", { count: n }),
              confirmLabel: t("usersPanel.disable"),
              danger: true,
            }
          : {
              title: t("usersPanel.resetTitle"),
              body: t("usersPanel.resetBody", { count: n }),
              confirmLabel: t("usersPanel.reset"),
              danger: true,
            };
    if (await confirm(opts)) runBulk(action);
  };

  // renderBulkActions builds the five action buttons shared by both bar layouts.
  // grid=true → full-width cells for the mobile 2-col grid; grid=false → inline
  // shrink-0 buttons for the desktop row. Only the in-flight action spins.
  const renderBulkActions = (grid: boolean) => {
    const cls = grid ? "" : "shrink-0";
    return [
      <Button key="enable" size="sm" variant="light" fullWidth={grid} className={cls} loading={pending === "enable"} disabled={pending !== null} onClick={() => confirmBulk("enable")}>
        {t("usersPanel.enable")}
      </Button>,
      <Button key="disable" size="sm" variant="light" color="gray" fullWidth={grid} className={cls} loading={pending === "disable"} disabled={pending !== null} onClick={() => confirmBulk("disable")}>
        {t("usersPanel.disable")}
      </Button>,
      <Button key="reset" size="sm" variant="light" color="gray" fullWidth={grid} className={cls} loading={pending === "reset"} disabled={pending !== null} onClick={() => confirmBulk("reset")}>
        {t("usersPanel.resetTraffic")}
      </Button>,
      <Button key="extend" size="sm" variant="light" fullWidth={grid} className={cls} disabled={pending !== null} onClick={() => setExtendOpen(true)}>
        {t("usersPanel.extend")}
      </Button>,
      <Button key="delete" size="sm" variant="light" color="red" fullWidth={grid} className={grid ? "col-span-2" : "shrink-0"} disabled={pending !== null} onClick={() => setConfirmDelete(true)}>
        {t("common.delete")}
      </Button>,
    ];
  };

  if (!loaded) return <UsersSkeleton />;

  if (users.length === 0) {
    return (
      <>
        <p className="py-12 text-center text-ink-muted">
          {t("usersPanel.emptyHint")}
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
            placeholder={t("groups.searchUsers")}
          />
        </div>
        <div className="sm:w-48 sm:shrink-0">
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            data={statusFilters()}
          />
        </div>
        {tagOptions.length > 0 && (
          <div className="sm:w-44 sm:shrink-0">
            <Select
              value={tagFilter}
              onChange={setTagFilter}
              data={[
                { value: "", label: t("usersPanel.anyTag") },
                ...tagOptions.map((o) => ({
                  value: o.tag,
                  label: t("usersPanel.tagN", { tag: o.tag, count: o.count }),
                })),
              ]}
            />
          </div>
        )}
        <div className="sm:w-48 sm:shrink-0">
          <Select value={sort} onChange={setSort} data={sorts()} />
        </div>
        <div className="sm:shrink-0">
          <ViewSwitch
            value={view}
            onChange={changeView}
            tableLabel={t("usersPanel.viewTable")}
            cardsLabel={t("usersPanel.viewCards")}
            label={t("nav.users")}
          />
        </div>
        {/* Icons, not words: this toolbar already carries a search box, two selects
            and the view switch, and these two are the least-used controls on it. The
            words live on as the accessible name and the hover title. */}
        <div className="flex gap-1 sm:shrink-0">
          <IconButton
            color="gray"
            onClick={() => setImportOpen(true)}
            title={t("importUsers.buttonHint")}
          >
            <IconImport />
          </IconButton>
          {/* A plain link, not a fetch: the file is an attachment the browser
              saves, and it carries every credential — no reason for it to pass
              through the SPA. */}
          <IconButton color="gray" href={exportUsersURL()} title={t("importUsers.exportHint")}>
            <IconExport />
          </IconButton>
        </div>
      </div>

      <div className="mb-3 flex items-center justify-between gap-3 text-sm text-ink-muted">
        <span>
          {filtered.length === users.length
            ? t("usersPanel.totalN", { count: users.length })
            : t("usersPanel.foundOf", { found: filtered.length, total: users.length })}
        </span>
        {filtered.length > 0 && (
          <button
            onClick={toggleAllFiltered}
            className="font-medium text-accent hover:underline"
          >
            {allFilteredSelected
              ? t("usersPanel.clearSelection")
              : t("usersPanel.selectAll", { count: filtered.length })}
          </button>
        )}
      </div>

      {filtered.length === 0 ? (
        <p className="py-12 text-center text-ink-muted">
          {t("usersPanel.noneMatch")}
        </p>
      ) : view === "table" ? (
        <UsersTable
          rows={paged}
          selected={selected}
          onToggleOne={toggleOne}
          onSetEnabled={(id, v) =>
            setUserEnabled(id, v)
              .then(refresh)
              .catch((e) => notifyError(errMessage(e)))
          }
          onDetail={setDetail}
        />
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {paged.map((u) => {
            const st = statusInfo(u.status);
            const checked = selected.has(u.id);
            return (
              <Card
                key={u.id}
                className={`p-4 ${checked ? "ring-2 ring-brand-600" : ""}`}
              >
                <div className="mb-3 flex min-w-0 items-center gap-2">
                  <SelectCheck
                    checked={checked}
                    onChange={(v) => toggleOne(u.id, v)}
                    label={t("usersPanel.selectUser", { name: u.name })}
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
                  <Code className="shrink-0 text-ink-muted">{u.system_email}</Code>
                </div>

                <div className="mb-3 flex flex-wrap gap-2">
                  <Badge color={st.color as never}>{st.label}</Badge>
                  <Badge color={isOnline(u.last_seen) ? "greenSolid" : "gray"}>
                    {isOnline(u.last_seen) ? t("usersPanel.online") : t("usersPanel.offline")}
                  </Badge>
                  <Badge color="brand">
                    {fmtQuota(u.used_up + u.used_down, u.data_limit)}
                  </Badge>
                  {u.expire_at > 0 && (
                    <Badge color="gray">{t("usersPanel.until", { date: fmtExpire(u.expire_at) })}</Badge>
                  )}
                  {u.device_limit > 0 && (
                    <Badge color={u.status === "device_limited" ? "orange" : "gray"}>
                      {t("usersPanel.devicesShort", { active: u.active_devices, limit: u.device_limit })}
                    </Badge>
                  )}
                  {(u.groups ?? []).map((g) => (
                    <Badge key={g.id} color="brand">
                      {g.name}
                    </Badge>
                  ))}
                  <TagList tags={u.tags ?? []} max={4} />
                </div>

                <div className="flex gap-2 mt-auto">
                  <Button
                    size="sm"
                    variant="light"
                    href={u.sub_url}
                    target="_blank"
                    className="flex-1"
                  >
                    {t("usersPanel.subscription")}
                  </Button>
                  <Button
                    size="sm"
                    variant="light"
                    color="gray"
                    onClick={() => setDetail(u)}
                    className="flex-1"
                  >
                    {t("usersPanel.details")}
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
            {t("common.back")}
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
            {t("usersPanel.forward")}
          </Button>
        </div>
      )}

      {/* The add-user FAB hides while a bulk selection is active (the bulk bar
          takes the bottom slot). */}
      {selected.size === 0 && <AddFab onClick={() => setAddOpen(true)} />}
      <ImportUsersModal open={importOpen} onClose={() => setImportOpen(false)} onImported={refresh} />

      {/* Reserve scroll space so the last cards aren't hidden behind the fixed
          selection bar (taller on mobile, where it stacks into a grid). The
          mobile/desktop switch uses the md breakpoint to match the header nav. */}
      {selected.size > 0 && <div aria-hidden className="h-44 md:h-20" />}

      {selected.size > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-40 border-t border-gray-200 bg-white/95 px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] shadow-lg backdrop-blur">
          <div className="mx-auto max-w-3xl">
            {/* Mobile: count + cancel on top, actions in a 2-col grid below — no
                horizontal scroll, and a fixed grid means the height can't jump
                when a button shows its spinner. md breakpoint matches the header. */}
            <div className="md:hidden">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-sm font-medium text-ink">
                  {t("usersPanel.selectedN", { count: selected.size })}
                </span>
                <button
                  onClick={clearSelection}
                  disabled={pending !== null}
                  className="text-sm font-medium text-ink-muted hover:text-ink disabled:opacity-60"
                >
                  {t("common.cancel")}
                </button>
              </div>
              <div className="grid grid-cols-2 gap-2">{renderBulkActions(true)}</div>
            </div>

            {/* Desktop: single row; actions scroll horizontally only if they don't
                fit, the label and Cancel stay pinned. md breakpoint matches the header. */}
            <div className="hidden items-center gap-2 md:flex">
              <span className="shrink-0 text-sm font-medium text-ink">
                {t("usersPanel.selectedN", { count: selected.size })}
              </span>
              <div className="flex min-w-0 flex-1 items-center gap-2 justify-center overflow-x-auto py-0.5">
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
                {t("common.cancel")}
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
        title={t("usersPanel.deleteTitle")}
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm text-ink-muted">
            {t("usersPanel.deleteBody", { count: selected.size })}
          </p>
          <div className="flex gap-2">
            <Button
              color="red"
              fullWidth
              loading={pending === "delete"}
              onClick={() => runBulk("delete")}
            >
              {t("usersPanel.deleteN", { count: selected.size })}
            </Button>
            <Button
              variant="subtle"
              color="gray"
              fullWidth
              onClick={() => setConfirmDelete(false)}
            >
              {t("common.cancel")}
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
        userBotEnabled={userBotEnabled}
        onChanged={refresh}
        onClose={() => {
          setDetail(null);
          refresh();
        }}
      />

      {confirmNode}
    </>
  );
}

// AddFab is the floating "+" button to add a user.
function AddFab({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      onClick={onClick}
      aria-label={t("usersPanel.addUser")}
      title={t("usersPanel.addUser")}
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
    <label className="flex shrink-0 cursor-pointer items-center" title={i18n.t("usersPanel.select")}>
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
  const { t } = useTranslation();
  const [days, setDays] = useState("30");
  const n = Math.floor(Number(days) || 0);
  return (
    <Modal open={open} onClose={onClose} title={t("usersPanel.extendTitle")}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-ink-muted">
          {t("usersPanel.extendBody", { count })}
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
              {t("usersPanel.plusDays", { count: p })}
            </Button>
          ))}
        </div>
        <TextInput
          label={t("usersPanel.days")}
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
            {n > 0
              ? t("usersPanel.extendByDays", { count: n })
              : t("usersPanel.extend")}
          </Button>
          <Button variant="subtle" color="gray" fullWidth onClick={onClose}>
            {t("common.cancel")}
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
  const { t } = useTranslation();
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
      title={t(created ? "usersPanel.userCreated" : "usersPanel.newUser")}
    >
      {!created ? (
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("usersPanel.name")}
            placeholder={t("usersPanel.namePlaceholder")}
            value={name}
            onChange={setName}
            autoFocus
          />
          <div className="grid grid-cols-2 gap-3">
            <DatePicker
              label={t("usersPanel.validUntil")}
              value={expDate}
              onChange={setExpDate}
              min={new Date().toISOString().slice(0, 10)}
            />
            <Select
              label={t("usersPanel.trafficLimit")}
              data={quotaOptions()}
              value={limitGb}
              onChange={setLimitGb}
            />
          </div>
          <Select
            label={t("usersPanel.autoReset")}
            data={resetPeriods()}
            value={resetPeriod}
            onChange={setResetPeriodState}
          />
          <Button loading={busy} onClick={submit}>
            {t("usersPanel.createAndShowLink")}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3">
          <p className="text-sm text-ink-muted">
            {t("usersPanel.subQrHint")}
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
            {t(copied ? "common.copied" : "usersPanel.copySubLink")}
          </Button>
          <Button
            variant="light"
            fullWidth
            href={created.sub_url}
            target="_blank"
          >
            {t("usersPanel.openSub")}
          </Button>
          <Button variant="subtle" color="gray" fullWidth onClick={close}>
            {t("common.done")}
          </Button>
        </div>
      )}
    </Modal>
  );
}



// UsersTable is the dense view: one row per user, the same controls the card carries.
//
// Narrow screens keep the columns that decide whether an account is healthy (name,
// status, traffic) and drop the rest — a horizontal scrollbar on a phone is worse than
// three fewer columns, and everything hidden here is one tap away in the detail sheet.
function UsersTable({
  rows,
  selected,
  onToggleOne,
  onSetEnabled,
  onDetail,
}: {
  rows: User[];
  selected: Set<number>;
  onToggleOne: (id: number, v: boolean) => void;
  onSetEnabled: (id: number, v: boolean) => void;
  onDetail: (u: User) => void;
}) {
  const { t } = useTranslation();
  return (
    <TableShell>
      <THead
        cols={[
          { srOnly: t("usersPanel.colSelect"), className: "w-10" },
          { label: t("usersPanel.colUser") },
          { label: t("usersPanel.colStatus") },
          { label: t("usersPanel.colOnline"), className: "hidden sm:table-cell" },
          { label: t("usersPanel.colTraffic") },
          { label: t("usersPanel.colExpires"), className: "hidden md:table-cell" },
          { label: t("usersPanel.colDevices"), className: "hidden lg:table-cell" },
          { label: t("usersPanel.colGroups"), className: "hidden lg:table-cell" },
          { label: t("usersPanel.colActions") },
        ]}
      />
        <tbody>
          {rows.map((u) => {
            const st = statusInfo(u.status);
            const checked = selected.has(u.id);
            return (
              <TR key={u.id} selected={checked}>
                <TD>
                  <SelectCheck
                    checked={checked}
                    onChange={(v) => onToggleOne(u.id, v)}
                    label={t("usersPanel.selectUser", { name: u.name })}
                  />
                </TD>
                <TD>
                  <div className="flex min-w-0 items-center gap-2">
                    <Switch checked={u.enabled} onChange={(v) => onSetEnabled(u.id, v)} />
                    <button
                      type="button"
                      onClick={() => onDetail(u)}
                      className={`truncate text-left font-medium hover:underline ${
                        u.status === "active" ? "text-ink" : "text-ink-muted"
                      }`}
                    >
                      {u.name}
                    </button>
                    {/* The Xray client id, so the operator can match a log line or a
                        stats row to the account without opening it. */}
                    <Code className="shrink-0 text-ink-muted">{u.system_email}</Code>
                    <TagList tags={u.tags ?? []} />
                  </div>
                </TD>
                <TD>
                  <Badge color={st.color as never}>{st.label}</Badge>
                </TD>
                <TD className="hidden sm:table-cell">
                  <Badge color={isOnline(u.last_seen) ? "greenSolid" : "gray"}>
                    {isOnline(u.last_seen) ? t("usersPanel.online") : t("usersPanel.offline")}
                  </Badge>
                </TD>
                <TD className="whitespace-nowrap">
                  {fmtQuota(u.used_up + u.used_down, u.data_limit)}
                </TD>
                <TD className="hidden whitespace-nowrap text-ink-muted md:table-cell">
                  {u.expire_at > 0 ? fmtExpire(u.expire_at) : "—"}
                </TD>
                <TD className="hidden whitespace-nowrap lg:table-cell">
                  {u.device_limit > 0 ? (
                    <span className={u.status === "device_limited" ? "text-warning" : ""}>
                      {t("usersPanel.devicesShort", {
                        active: u.active_devices,
                        limit: u.device_limit,
                      })}
                    </span>
                  ) : (
                    <span className="text-ink-muted">—</span>
                  )}
                </TD>
                <TD className="hidden lg:table-cell">
                  <GroupCell groups={u.groups ?? []} />
                </TD>
                <TD>
                  {/* Below sm only the subscription link rides here: two buttons push the
                      row past a phone's width, and the wrapper would answer with the
                      horizontal scrollbar this layout exists to avoid. Details stays
                      reachable — the name is the button that opens it. */}
                  {/* Icons, not words: two labelled buttons per row was most of the
                      row's width for the two things every row repeats. Two 32px icons
                      fit at every width, so neither hides on a phone. The words live on
                      as the accessible name and the hover title. */}
                  <div className="flex gap-1">
                    <IconButton
                      href={u.sub_url}
                      target="_blank"
                      color="brand"
                      title={t("usersPanel.subscription")}
                    >
                      <IconExternal size={16} />
                    </IconButton>
                    <IconButton
                      onClick={() => onDetail(u)}
                      title={t("usersPanel.details")}
                    >
                      <IconEye size={16} />
                    </IconButton>
                  </div>
                </TD>
              </TR>
            );
          })}
      </tbody>
    </TableShell>
  );
}

// tagsShown caps how many tag badges a row renders next to the name; the rest fold
// into "+N" with the full list on hover, for the same reason groupsShown does below.
const tagsShown = 2;

// TagList renders a user's tags as small muted badges — muted so they read as labels
// the operator attached, not as a status the panel derived.
function TagList({ tags, max = tagsShown }: { tags: string[]; max?: number }) {
  if (tags.length === 0) return null;
  const shown = tags.slice(0, max);
  const rest = tags.slice(max);
  return (
    <>
      {shown.map((tag) => (
        <Badge key={tag} color="gray" size="xs" className="shrink-0">
          {tag}
        </Badge>
      ))}
      {rest.length > 0 && (
        <Badge color="gray" size="xs" className="shrink-0" title={rest.join(", ")}>
          +{rest.length}
        </Badge>
      )}
    </>
  );
}

// groupsShown is how many group badges a table cell renders before collapsing the rest
// into a count. One, because the column is the widest thing in the row that nobody reads
// most of the time: a user in four groups would otherwise stretch every row on the page
// to fit the account with the longest list.
const groupsShown = 1;

// GroupCell renders a user's groups compactly: the first, then "+N" carrying the rest as
// hover text. Native title, like every other hint in this panel — there is no tooltip
// component here, and inventing one for a count would be the odd thing out.
function GroupCell({ groups }: { groups: { id: number; name: string }[] }) {
  if (groups.length === 0) {
    return <span className="text-ink-muted">—</span>;
  }
  const shown = groups.slice(0, groupsShown);
  const rest = groups.slice(groupsShown);
  return (
    <div className="flex items-center gap-1 whitespace-nowrap">
      {shown.map((g) => (
        <Badge key={g.id} color="brand">
          {g.name}
        </Badge>
      ))}
      {rest.length > 0 && (
        <Badge color="gray" title={rest.map((g) => g.name).join(", ")}>
          +{rest.length}
          {/* A native title on a span reaches neither the keyboard nor a screen reader,
              and this column only shows on desktop — where keyboard users are. */}
          <span className="sr-only">{rest.map((g) => g.name).join(", ")}</span>
        </Badge>
      )}
    </div>
  );
}
