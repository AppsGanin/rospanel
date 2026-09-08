import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type BulkAction,
  bulkUsers,
  createUser,
  listUsers,
  setResetPeriod,
  setUserEnabled,
  type User,
} from "./api";
import { useAction, useShowMore } from "./hooks";
import i18n, { currentLang } from "./i18n";
import {
  dateToUnixEndOfDay,
  fmtExpire,
  fmtQuota,
  gbToBytes,
  isOnline,
  quotaOptions,
  resetPeriods,
  statusInfo,
  unixToLocalDate,
} from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  cn,
  Code,
  DatePicker,
  Dropdown,
  DropdownItem,
  EmptyState,
  IconCheck,
  IconSearch,
  Modal,
  IconButton,
  loadColor,
  MICRO,
  Mono,
  Panel,
  Select,
  Skeleton,
  TextInput,
  useConfirm,
  useCopy,
  useWideBox,
} from "./ui";
import { UserDetail } from "./UserDetail";
import { ImportUsersModal } from "./ImportUsersModal";

// COLS is the row: checkbox, who, state, traffic, expiry, devices, group, actions.
// One template for the header and every row — a column cannot drift away from its
// heading. Below sm the row collapses to two columns and reads as a card, which is
// the design's single rule for dense tables.
// TPL is the row: checkbox, who, state, traffic, expiry, devices, group, actions.
// Declared once and used by the header and every row — two copies drift apart.
const TPL =
  "28px minmax(0,2.1fr) minmax(0,1fr) minmax(0,1.4fr) minmax(0,.9fr) minmax(0,.9fr) minmax(0,.9fr)";
// Between the phone layout and the full table there is a middle: wide enough for
// columns, too narrow for seven of them — «устройства» and «группы» are the two the
// drawer answers anyway, so they are the two that go.
const TPL_MID =
  "28px minmax(0,2.1fr) minmax(0,1fr) minmax(0,1.4fr) minmax(0,.9fr)";
// On a phone the same cells fold into two lines: who on the first, how they are and
// what they have spent on the second.
const TPL_MOBILE = "28px minmax(0,1fr) auto";

// The width the seven-column table needs before it is worth showing: below it the
// header words alone («пользователь», «устройства») are wider than their tracks.
const LIST_WIDE_MIN = 520;
// Seven columns need room for the word «устройства» — 800px of list is where the
// heading stops being clipped. Below it the table drops to five.
const LIST_ROOMY_MIN = 800;

// On a phone the same row folds into three lines in the second column — who, what
// state, how much traffic — with the checkbox and the two actions pinned to the
// first line. Placement only: the markup is identical at every width, which is what
// keeps one row component instead of two.
// Where the three cells the phone layout keeps go in its 3x2 grid. Placement is
// explicit because the row's other cells are simply not rendered there, and an auto
// flow would slide the name into the checkbox's column.
const M_NAME = "col-start-2 col-span-2 row-start-1";
const M_STATUS = "col-start-2 row-start-2";
const M_TRAFFIC = "col-start-3 row-start-2 flex justify-end";

const EXTEND_PRESETS = [7, 30, 90, 180];

// The list is chunked rather than paged: these are rows an operator scrolls, and a
// page number is a second thing to keep track of for no gain.
const FIRST_CHUNK = 50;

// EXPIRY_SOON_DAYS is what the "expiring" filter means, and it matches the
// dashboard's own tile — the two must count the same accounts.
const EXPIRY_SOON_DAYS = 7;

// expSortKey orders by soonest expiry; "never" (0) sorts last.
const expSortKey = (u: User) => (u.expire_at > 0 ? u.expire_at : Infinity);

const sorts = () => [
  { value: "new", label: i18n.t("usersPanel.sNew") },
  { value: "name", label: i18n.t("usersPanel.sName") },
  { value: "traffic", label: i18n.t("usersPanel.sTraffic") },
  { value: "expiry", label: i18n.t("usersPanel.sExpiry") },
  { value: "online", label: i18n.t("usersPanel.sOnline") },
];

// Filter is what the chips above the list select. It is wider than User.status on
// purpose: "online" and "expiring" are facts about a user that no status field
// carries, and they are the two an operator filters by most.
type Filter =
  | "all"
  | "active"
  | "online"
  | "expiring"
  | "limited"
  | "disabled"
  | "expired";

const CHIPS: { value: Filter; key: string }[] = [
  { value: "active", key: "usersPanel.chipActive" },
  { value: "online", key: "usersPanel.chipOnline" },
  { value: "expiring", key: "usersPanel.chipExpiring" },
  { value: "limited", key: "usersPanel.chipLimited" },
  { value: "disabled", key: "usersPanel.chipDisabled" },
  { value: "expired", key: "usersPanel.chipExpired" },
];

function matches(u: User, f: Filter, now: number): boolean {
  switch (f) {
    case "active":
      return u.status === "active";
    case "online":
      return isOnline(u.last_seen);
    case "expiring":
      return (
        u.expire_at > 0 &&
        u.expire_at > now &&
        u.expire_at - now <= EXPIRY_SOON_DAYS * 86400
      );
    case "limited":
      return u.status === "limited" || u.status === "device_limited";
    case "disabled":
      return u.status === "disabled";
    case "expired":
      return u.status === "expired";
    default:
      return true;
  }
}

// The skeleton repeats the shape of the table, so the page does not rebuild itself
// under the operator when the list lands.
function UsersSkeleton() {
  return (
    <div className="flex animate-fade-in flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Skeleton className="h-8 w-65 rounded-lg" />
        <Skeleton className="h-7 w-24 rounded-full" />
        <Skeleton className="h-7 w-20 rounded-full" />
        <Skeleton className="h-7 w-24 rounded-full" />
      </div>
      <Panel>
        {[...Array(8)].map((_, i) => (
          <div key={i} className="border-b border-gray-100 px-3.5 py-2.5 last:border-0">
            <Skeleton className="h-3.5 w-full" />
          </div>
        ))}
      </Panel>
    </div>
  );
}

// FilterChip is one of the toolbar's saved questions: "which of these are online",
// "which run out this week". Selected reads as an accent-tinted pill; the count is
// part of the label because the answer is worth knowing before clicking.
function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "shrink-0 rounded-full border px-3 py-1.5 text-xs font-medium transition",
        active
          ? "accent-tint border-accent text-accent"
          : "border-gray-300 text-ink-muted hover:bg-gray-50",
      )}
    >
      {label}
      {count !== undefined && (
        <span className="ml-1 font-mono opacity-70">· {count}</span>
      )}
    </button>
  );
}

export function UsersPanel({
  userBotEnabled,
  addOpen,
  onAddOpen,
  importOpen,
  onImportOpen,
  onTotal,
}: {
  userBotEnabled: boolean;
  addOpen: boolean;
  onAddOpen: (v: boolean) => void;
  importOpen: boolean;
  onImportOpen: (v: boolean) => void;
  onTotal: (n: number) => void;
}) {
  const { t } = useTranslation();
  const [users, setUsers] = useState<User[]>([]);
  const [detail, setDetail] = useState<User | null>(null);
  const [loaded, setLoaded] = useState(false);

  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  // "" = any tag. Derived from the loaded list rather than fetched: the list already
  // carries every user's tags, so the dropdown can never disagree with the rows.
  const [tagFilter, setTagFilter] = useState("");
  const [sort, setSort] = useState("new");

  const [selected, setSelected] = useState<Set<number>>(new Set());
  // pending is the bulk action currently in flight (null = none). Tracking the
  // specific action lets only the clicked button show a spinner, and keeps the
  // action bar from reflowing/jumping while one runs.
  const [pending, setPending] = useState<BulkAction | null>(null);
  const [extendOpen, setExtendOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { confirm, confirmNode } = useConfirm();
  // The columns have to fit the LIST, not the window: with the sidebar showing, a
  // 700px window leaves the table ~430px. Below this the row switches to its
  // three-cell phone layout instead of scrolling sideways.
  const [listRef, wide, listWidth] = useWideBox(LIST_WIDE_MIN);
  const roomy = listWidth >= LIST_ROOMY_MIN;

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

  useEffect(() => {
    onTotal(users.length);
  }, [users.length, onTotal]);

  const now = Date.now() / 1000;

  // Filtering and sorting are client-side: the full list is already loaded and stays
  // snappy well into the hundreds, so this avoids any API round-trips.
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return users.filter(
      (u) =>
        matches(u, filter, now) &&
        (tagFilter === "" || (u.tags ?? []).includes(tagFilter)) &&
        (q === "" ||
          u.name.toLowerCase().includes(q) ||
          String(u.id) === q ||
          u.system_email.toLowerCase() === q ||
          (u.note ?? "").toLowerCase().includes(q) ||
          (u.tags ?? []).some((tag) => tag.includes(q))),
    );
    // `now` deliberately out of the deps: it changes on every render and only the
    // "expiring" chip reads it, where a second's drift cannot change the answer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [users, query, filter, tagFilter]);

  // What each chip would find, so the operator can see the shape of the list before
  // clicking. Counted over every user, not over the current filter — a chip that
  // counted only what is already on screen would always read the same.
  const counts = useMemo(() => {
    const out = {} as Record<Filter, number>;
    for (const c of CHIPS) out[c.value] = users.filter((u) => matches(u, c.value, now)).length;
    out.all = users.length;
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [users]);

  // Every tag in use with how many users carry it, most used first, for the
  // toolbar filter. A tag that disappears from every user drops out of the list,
  // and a filter pinned to it falls back to "any" in the effect below.
  const tagOptions = useMemo(() => {
    const map = new Map<string, number>();
    for (const u of users) for (const tag of u.tags ?? []) map.set(tag, (map.get(tag) ?? 0) + 1);
    return [...map.entries()]
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
        arr.sort((a, b) => b.used_up + b.used_down - (a.used_up + a.used_down));
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

  // Chunked, not paged: `resetKey` collapses back to the first chunk whenever the
  // result set becomes about something else.
  const { shown, rest, showMore } = useShowMore(sorted, {
    first: FIRST_CHUNK,
    step: FIRST_CHUNK,
    resetKey: `${query}|${filter}|${tagFilter}|${sort}`,
  });

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
  const clearFilters = () => {
    setQuery("");
    setFilter("all");
    setTagFilter("");
  };
  const setEnabled = (id: number, v: boolean) =>
    setUserEnabled(id, v)
      .then(refresh)
      .catch((e) => notifyError(errMessage(e)));

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

  if (!loaded) return <UsersSkeleton />;

  const hasFilter = query !== "" || filter !== "all" || tagFilter !== "";

  const addButton = (
    <Button size="sm" onClick={() => onAddOpen(true)}>
      {t("common.add")}
    </Button>
  );

  const dialogs = (
    <>
      <ImportUsersModal
        open={importOpen}
        onClose={() => onImportOpen(false)}
        onImported={refresh}
      />
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
          onAddOpen(false);
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

  // Nothing created yet is a different screen from "your filter matched nothing":
  // one needs a first user, the other needs the filter cleared.
  if (users.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <EmptyState
          title={t("usersPanel.emptyTitle")}
          body={t("usersPanel.emptyBody")}
          action={addButton}
        />
        {dialogs}
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* One line: a compact search, the saved questions as chips, and the sort as
          text. Boxed selects for the tag and the sort made this two rows of controls
          taller than the rows they filter. */}
      <div className="flex flex-wrap items-center gap-2 border-b border-brand-600/10 px-5 py-2.5">
        <span className="flex w-65 max-w-full items-center gap-2 rounded-lg border border-gray-300 bg-gray-50 px-2.5 py-1.5">
          <IconSearch size={14} className="shrink-0 text-ink-muted" />
          <input
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            placeholder={t("usersPanel.searchPlaceholder")}
            className="min-w-0 flex-1 bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-muted"
          />
        </span>

        <FilterChip
          label={t("usersPanel.chipAll")}
          count={counts.all}
          active={filter === "all"}
          onClick={() => setFilter("all")}
        />
        {/* A chip nothing matches is a question with no answer — it is not drawn. */}
        {CHIPS.filter((c) => counts[c.value] > 0).map((c) => (
          <FilterChip
            key={c.value}
            label={t(c.key as "usersPanel.chipActive")}
            count={counts[c.value]}
            active={filter === c.value}
            onClick={() => setFilter(filter === c.value ? "all" : c.value)}
          />
        ))}

        {tagOptions.length > 0 && (
          <Dropdown
            align="start"
            width={220}
            trigger={
              <span
                className={cn(
                  "shrink-0 rounded-full border px-3 py-1.5 text-xs font-medium transition",
                  tagFilter
                    ? "accent-tint border-accent text-accent"
                    : "border-gray-300 text-ink-muted hover:bg-gray-50",
                )}
              >
                {tagFilter || t("usersPanel.addTagFilter")}
              </span>
            }
          >
            {tagFilter && (
              <DropdownItem onClick={() => setTagFilter("")}>
                {t("usersPanel.anyTag")}
              </DropdownItem>
            )}
            {tagOptions.map((o) => (
              <DropdownItem key={o.tag} onClick={() => setTagFilter(o.tag)}>
                {t("usersPanel.tagN", { tag: o.tag, count: o.count })}
              </DropdownItem>
            ))}
          </Dropdown>
        )}

        <div className="ml-auto flex items-center gap-2">
          <Dropdown
            width={200}
            trigger={
              <span className="whitespace-nowrap text-xs text-ink-muted transition hover:text-ink">
                {t("usersPanel.sortBy", {
                  name: sorts().find((o) => o.value === sort)?.label ?? "",
                })}
              </span>
            }
          >
            {sorts().map((o) => (
              <DropdownItem key={o.value} onClick={() => setSort(o.value)}>
                <span className={o.value === sort ? "font-medium text-accent" : ""}>
                  {o.label}
                </span>
              </DropdownItem>
            ))}
          </Dropdown>
        </div>
      </div>

      <div ref={listRef} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {filtered.length === 0 ? (
          <EmptyState
            title={t("usersPanel.notFoundTitle")}
            body={t("usersPanel.notFoundBody")}
            action={
              <Button size="sm" variant="outline" onClick={clearFilters}>
                {t("usersPanel.resetFilters")}
              </Button>
            }
          />
        ) : (
          <>
          {wide && (
          <div
            className={cn(
              MICRO,
              "grid items-center gap-3 border-b border-brand-600/10 px-5 py-2",
            )}
            style={{ gridTemplateColumns: roomy ? TPL : TPL_MID }}
          >
            <SelectCheck
              checked={allFilteredSelected}
              onChange={toggleAllFiltered}
              label={t("usersPanel.selectAll", { count: filtered.length })}
            />
            <span className="truncate">{t("usersPanel.colUser")}</span>
            <span className="truncate">{t("usersPanel.colStatus")}</span>
            <span className="truncate">{t("usersPanel.colTraffic")}</span>
            <span className="truncate">{t("usersPanel.colExpires")}</span>
            {roomy && (
              <>
                <span className="truncate">{t("usersPanel.colDevices")}</span>
                <span className="truncate">{t("usersPanel.colGroups")}</span>
              </>
            )}
          </div>
          )}
          {shown.map((u) => (
            <UserRow
              key={u.id}
              u={u}
              wide={wide}
              roomy={roomy}
              checked={selected.has(u.id)}
              onToggle={(v) => toggleOne(u.id, v)}
              onSetEnabled={(v) => setEnabled(u.id, v)}
              onDetail={() => setDetail(u)}
            />
          ))}
          </>
        )}

        <div aria-hidden className="flex-1" />
      </div>

      {/* The footer of the list, sticky so it stays reachable while scrolling: with a
          selection it carries the bulk actions, without one it carries the count and
          the way to see more. With everything already on screen and nothing selected
          there is nothing to say — "showing 5 of 5" is a bar to report a bar. */}
      {(selected.size > 0 || rest > 0) && (
        <div className="sticky bottom-0 z-10 border-t border-brand-600/10 bg-gray-50/95 px-5 py-2.5 backdrop-blur">
          {selected.size > 0 ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[13px] font-semibold text-ink">
                {t("usersPanel.selectedN", { count: selected.size })}
              </span>
              <Button size="xs" variant="outline" color="gray" disabled={pending !== null} loading={pending === "enable"} onClick={() => confirmBulk("enable")}>
                {t("usersPanel.enable")}
              </Button>
              <Button size="xs" variant="outline" color="gray" disabled={pending !== null} loading={pending === "disable"} onClick={() => confirmBulk("disable")}>
                {t("usersPanel.disable")}
              </Button>
              <Button size="xs" variant="outline" color="gray" disabled={pending !== null} onClick={() => setExtendOpen(true)}>
                {t("usersPanel.extend")}
              </Button>
              <Button size="xs" variant="outline" color="gray" disabled={pending !== null} loading={pending === "reset"} onClick={() => confirmBulk("reset")}>
                {t("usersPanel.resetTraffic")}
              </Button>
              <Button size="xs" variant="outline" color="red" disabled={pending !== null} onClick={() => setConfirmDelete(true)}>
                {t("common.delete")}
              </Button>
              <button
                onClick={clearSelection}
                disabled={pending !== null}
                className="text-xs font-medium text-accent hover:underline disabled:opacity-60"
              >
                {t("usersPanel.clearSelection")}
              </button>
              <span className="ml-auto text-xs text-ink-muted">
                {t("usersPanel.shownOf", { shown: shown.length, total: users.length })}
              </span>
            </div>
          ) : (
            <div className="flex items-center gap-3">
              <span className="text-xs text-ink-muted">
                {t("usersPanel.shownOf", { shown: shown.length, total: users.length })}
              </span>
              {rest > 0 && (
                <Button
                  size="xs"
                  variant="outline"
                  color="gray"
                  className="ml-auto"
                  onClick={showMore}
                >
                  {t("common.showMoreCount", { n: Math.min(rest, FIRST_CHUNK) })}
                </Button>
              )}
            </div>
          )}
        </div>
      )}

      {dialogs}
    </div>
  );
}

// SelectCheck is the 16px selection box: a real (screen-reader visible) input drives
// a box drawn with the panel's own tokens, so it follows the operator's theme instead
// of the browser's white default.
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
    <label
      className="relative flex shrink-0 cursor-pointer items-center"
      title={i18n.t("usersPanel.select")}
    >
      <input
        type="checkbox"
        className="sr-only"
        checked={checked}
        aria-label={label}
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
      <span
        className={cn(
          "flex size-4 items-center justify-center rounded-sm border transition",
          checked
            ? "border-brand-600 bg-brand-600 text-onaccent"
            : "border-gray-300 bg-white hover:border-gray-400",
        )}
      >
        {checked && <IconCheck size={12} />}
      </span>
    </label>
  );
}

// statusTone maps a user's state to how it should read. The word carries the state;
// the colour only repeats it.
function statusTone(status: string): string {
  switch (status) {
    case "active":
      return "text-success";
    case "limited":
    case "device_limited":
      return "text-warning";
    case "expired":
      return "text-danger";
    default:
      return "text-ink-muted";
  }
}

// TrafficCell is used-against-limit at a glance: a 52px bar and the figures. Without
// a limit there is nothing to fill, so it is the figure alone — a full bar for
// "unlimited" would read as an account about to be cut off.
function TrafficCell({ u }: { u: User }) {
  const used = u.used_up + u.used_down;
  const pct = u.data_limit > 0 ? Math.min(100, (used / u.data_limit) * 100) : 0;
  return (
    <span className="flex min-w-0 items-center gap-2">
      <span className="h-1 w-13 shrink-0 overflow-hidden rounded-full bg-gray-200">
        <span
          className={cn("block h-full rounded-full", loadColor(pct))}
          style={{ width: `${pct}%` }}
        />
      </span>
      <Mono className="truncate text-[11px] text-ink-muted">
        {fmtQuota(used, u.data_limit)}
      </Mono>
    </span>
  );
}

// UserRow is one account in the dense view. The online dot leads the name (a column
// of its own for two states was a column of mostly-empty cells); everything else is
// the fact and its unit, mono where it is a number.
function UserRow({
  u,
  wide,
  roomy,
  checked,
  onToggle,
  onSetEnabled,
  onDetail,
}: {
  u: User;
  wide: boolean;
  roomy: boolean;
  checked: boolean;
  onToggle: (v: boolean) => void;
  onSetEnabled: (v: boolean) => void;
  onDetail: () => void;
}) {
  const { t } = useTranslation();
  const st = statusInfo(u.status);
  const online = isOnline(u.last_seen);
  const groups = u.groups ?? [];
  // A row against a limit is the one an operator is looking for; it says so quietly,
  // under text that still has to read as text.
  const limited = u.status === "limited" || u.status === "device_limited";
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onDetail}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onDetail();
        }
      }}
      className={cn(
        "grid cursor-pointer items-center gap-3 border-b border-gray-100 px-5 py-[7px] transition last:border-0",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-100",
        checked
          ? "accent-tint"
          : limited
            ? "warning-tint"
            : "hover:bg-gray-50",
      )}
      style={{ gridTemplateColumns: wide ? (roomy ? TPL : TPL_MID) : TPL_MOBILE }}
    >
      {/* Selecting is not opening: the box swallows the click that would open the card. */}
      <span className={wide ? "" : "row-start-1"} onClick={(e) => e.stopPropagation()}>
        <SelectCheck
          checked={checked}
          onChange={onToggle}
          label={t("usersPanel.selectUser", { name: u.name })}
        />
      </span>

      <span className={cn("flex min-w-0 items-center gap-2", !wide && M_NAME)}>
        <span
          className={cn("size-1.5 shrink-0 rounded-full", online ? "bg-success" : "bg-gray-300")}
          title={t(online ? "usersPanel.online" : "usersPanel.offline")}
        />
        <span
          className={cn(
            "truncate text-[13px] font-medium",
            u.status === "active" ? "text-ink" : "text-ink-muted",
          )}
        >
          {u.name}
        </span>
        {/* The Xray client id, so the operator can match a log line or a stats row to
            the account without opening it. */}
        <Mono className="shrink-0 text-[11px] text-ink-muted">{u.system_email}</Mono>
        <TagList tags={u.tags ?? []} />
      </span>

      <span className={cn("truncate text-xs", !wide && M_STATUS, statusTone(u.status))}>
        {st.label}
      </span>

      <span className={!wide ? M_TRAFFIC : undefined}>
        <TrafficCell u={u} />
      </span>

      {/* The tail columns exist only where they fit — narrow they are not hidden but
          absent, so nothing has to be placed around them. */}
      {wide && (
        <Mono className="truncate text-[11px] text-ink-muted">
          {u.expire_at > 0 ? fmtExpire(u.expire_at) : "—"}
        </Mono>
      )}

      {wide && roomy && (
        <Mono
          className={cn(
            "truncate text-[11px]",
            u.status === "device_limited" ? "text-warning" : "text-ink-muted",
          )}
        >
          {u.device_limit > 0 ? `${u.active_devices}/${u.device_limit}` : "—"}
        </Mono>
      )}

      {wide && roomy && (
        <span className="truncate text-xs text-accent">
          {groups.length === 0 ? (
            <span className="text-ink-muted">—</span>
          ) : (
            <span title={groups.map((g) => g.name).join(", ")}>
              {groups[0].name}
              {groups.length > 1 && ` +${groups.length - 1}`}
            </span>
          )}
        </span>
      )}

    </div>
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
      const ea = dateToUnixEndOfDay(expDate);
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
              min={unixToLocalDate(Math.floor(Date.now() / 1000))}
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

// tagsShown caps how many tag badges a row renders next to the name; the rest fold
// into "+N" with the full list on hover, for the same reason groups collapse.
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
