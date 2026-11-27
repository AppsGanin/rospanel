import { PluginActionButtons } from "./PluginSurfaces";
import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type BulkAction,
  bulkUsers,
  createUser,
  getBilling,
  type Group,
  getUser,
  listGroups,
  listUsersPage,
  setResetPeriod,
  setUserPlan,
  type TariffPlan,
  type User,
  type UserRow,
  type UsersPage,
} from "./api";
import { useAction, useDebounced } from "./hooks";
import i18n, { currentLang } from "./i18n";
import {
  dateToUnixEndOfDay,
  fmtExpire,
  fmtQuota,
  fmtTerm,
  gbToBytes,
  isOnline,
  quotaOptions,
  resetPeriods,
  statusInfo,
  termModes,
  unixToLocalDate,
} from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan } from "./role";
import {
  Button,
  cn,
  Code,
  DatePicker,
  Dropdown,
  EmptyState,
  IconCheck,
  IconChevron,
  IconClose,
  IconSearch,
  Modal,
  loadColor,
  MICRO,
  Mono,
  Select,
  Skeleton,
  Skeletons,
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
// page number is a second thing to keep track of for no gain. Each chunk is a request:
// the server filters, sorts and counts, and sends only the rows asked for.
const FIRST_CHUNK = 50;
// The most rows one request brings back (the server's own cap) — a refresh reloads
// what is on screen in one go, up to this.
const WINDOW_MAX = 1000;

// sortColumns are the columns that carry an order, and the direction each reads in
// first: a name list reads forwards, money and dates read newest and largest first.
const sortColumns: Record<string, "asc" | "desc"> = {
  name: "asc",
  traffic: "desc",
  expiry: "asc",
};

// SortHead is one clickable column title. The first click orders by the column in its
// natural direction, the second turns it around, the third puts the list back in its
// own newest-first order — the state a header shows with no arrow at all.
function SortHead({
  id,
  label,
  sort,
  dir,
  onSort,
}: {
  id: string
  label: string
  sort: string
  dir: string
  onSort: (id: string) => void
}) {
  const active = sort === id
  return (
    <button
      type="button"
      onClick={() => onSort(id)}
      className={cn(
        "flex min-w-0 items-center gap-1 text-start transition",
        active ? "text-ink" : "hover:text-ink",
      )}
    >
      <span className="truncate">{label}</span>
      <span aria-hidden className={cn("shrink-0", active ? "text-accent" : "opacity-30")}>
        {active && dir === "asc" ? "↑" : active ? "↓" : "↕"}
      </span>
    </button>
  )
}

// Filter is what the chips above the list select. It is wider than User.status on
// purpose: "online" and "expiring" are facts about a user that no status field
// carries, and they are the two an operator filters by most. The server decides who
// falls under each; "expiring" means the same seven days as the dashboard's tile.
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

// selectionLabel is how the bar names a multi-choice: the first name and how many
// more there are — "VIP +2", which says what is on and that there is more of it.
function selectionLabel(names: (string | undefined)[]): string | undefined {
  const shown = names.filter((n): n is string => !!n)
  if (shown.length === 0) return undefined
  if (shown.length === 1) return shown[0]
  return `${shown[0]} +${shown.length - 1}`
}

// chipLabel is the name of one status as the bar prints it, empty for no status.
function chipLabel(value?: Filter): string {
  if (!value) return ""
  const key = (CHIPS.find((c) => c.value === value)?.key ?? "usersPanel.chipAll") as
    | "usersPanel.chipAll"
    | "usersPanel.chipActive"
  return i18n.t(key)
}

// The skeleton repeats the shape of the table — the same full-bleed toolbar and
// rows, with the same insets — so the page does not rebuild itself under the
// operator when the list lands.
function UsersSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 animate-fade-in flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-brand-600/10 bg-gray-50/70 px-5 py-2.5">
        <Skeleton className="h-8 w-56 max-w-full rounded-lg" />
        <Skeleton className="h-7.5 w-20 rounded-full" />
        <Skeleton className="h-7.5 w-24 rounded-full" />
      </div>
      <div className="border-b border-brand-600/10 px-5 py-2.5">
        <Skeleton className="h-3 w-1/3" />
      </div>
      <Skeletons n={8} row="border-b border-gray-100 px-5 py-2.5 last:border-0" className="h-4 w-full" />
    </div>
  );
}

// toggle adds a value to a selection or takes it out.
function toggle<T>(list: T[], v: T): T[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
}

// MenuItem is one row of a filter menu: a tick, the name and how many users carry it.
// It does not close the menu — the point of these menus is picking several things.
function MenuItem({
  checked,
  label,
  count,
  onClick,
}: {
  checked: boolean
  label: string
  count?: number
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={checked}
      className="flex w-full items-center gap-2 px-3 py-1.5 text-start text-xs transition hover:bg-gray-50"
    >
      <span
        aria-hidden
        className={cn(
          "flex size-3.5 shrink-0 items-center justify-center rounded border text-[9px]",
          checked ? "border-accent bg-accent text-onbrand" : "border-gray-300",
        )}
      >
        {checked ? "✓" : ""}
      </span>
      <span className={cn("min-w-0 flex-1 truncate", checked ? "text-ink" : "text-ink-muted")}>
        {label}
      </span>
      {count !== undefined && <span className="font-mono text-[11px] text-ink-muted">{count}</span>}
    </button>
  )
}

// BarButton is the trigger of every menu the filter bar offers — a tag, a group, the
// order. One look: the attribute's name in muted text, its value beside it, a chevron;
// accent-tinted once a value is set, so the bar says at a glance what narrows it.
// (It is a span: Dropdown already wraps its trigger in a button.)
function BarButton({
  label,
  value,
  placeholder,
}: {
  label: string
  value?: string
  // What "no value" reads as; the name alone when there is nothing to say.
  placeholder?: string
}) {
  const shown = value ?? placeholder
  return (
    <span
      className={cn(
        "inline-flex h-8 max-w-56 shrink-0 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition",
        value
          ? "accent-tint border-accent text-accent"
          : "border-gray-200 bg-white text-ink-muted hover:border-gray-300 hover:text-ink",
      )}
    >
      {shown ? (
        <>
          <span className={cn(value ? "text-accent/70" : "text-ink-muted")}>{label}</span>
          <span className={cn("truncate font-medium", value ? "text-accent" : "text-ink-muted")}>
            {shown}
          </span>
        </>
      ) : (
        <span className="font-medium">{label}</span>
      )}
      <IconChevron size={12} className="shrink-0 opacity-60" />
    </span>
  )
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
  // page is the latest answer — the counts, the tags and how many match; rows are the
  // lines loaded so far under the current filter, one chunk after another.
  const [page, setPage] = useState<UsersPage | null>(null);
  const [rows, setRows] = useState<UserRow[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [detail, setDetail] = useState<User | null>(null);
  const [loaded, setLoaded] = useState(false);

  const [query, setQuery] = useState("");
  const search = useDebounced(query.trim(), 250);
  // Any of these statuses (none = everyone); the bar's "Статус" menu sets them.
  const [filters, setFilters] = useState<Filter[]>([]);
  // "" = any tag. The options come with every page, counted over all users, so the
  // dropdown can never disagree with the rows.
  const [tagFilter, setTagFilter] = useState<string[]>([]);
  // "" = any group, "none" = users in none, else the group's id as a string.
  const [groupFilter, setGroupFilter] = useState<string[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  // The order the table's header sets: a column and its direction. "new" — the list's
  // own newest-first order — is what an untouched table shows.
  const [sort, setSort] = useState("new");
  const [sortDir, setSortDir] = useState("");
  const filterKey = `${search}|${filters.join(",")}|${tagFilter.join(",")}|${groupFilter.join(",")}|${sort}|${sortDir}`;

  const canManage = useCan("users.manage");
  const canDelete = useCan("users.delete");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // Every id the current filter matches, fetched when "select all" is pressed: the
  // rows on screen are only the chunks loaded so far.
  const [matchIds, setMatchIds] = useState<{ key: string; ids: number[] } | null>(null);
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

  // seq numbers the requests that replace the list, so an answer that arrives after
  // the filter has moved on is dropped instead of drawn.
  const seq = useRef(0);
  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  const detailRef = useRef(detail);
  detailRef.current = detail;
  const filterKeyRef = useRef(filterKey);
  filterKeyRef.current = filterKey;

  const fetchWindow = useCallback(
    (offset: number, limit: number) =>
      listUsersPage({
        q: search,
        filter: filters,
        tag: tagFilter,
        group: groupFilter,
        sort,
        dir: sortDir,
        lang: currentLang(),
        offset,
        limit,
      }),
    [search, filters, tagFilter, groupFilter, sort, sortDir],
  );

  // load replaces the list with its first `limit` rows under the current filter.
  const load = useCallback(
    (limit: number) => {
      const my = ++seq.current;
      return fetchWindow(0, limit)
        .then((p) => {
          if (my !== seq.current) return;
          setPage(p);
          setRows(p.users);
        })
        .catch(() => {})
        .finally(() => {
          if (my === seq.current) setLoaded(true);
        });
    },
    [fetchWindow],
  );

  // A new question starts from the first chunk.
  useEffect(() => {
    load(FIRST_CHUNK);
  }, [load]);

  // refresh reloads what is on screen after a change, and the open card with it.
  const refresh = useCallback(() => {
    setMatchIds(null);
    load(Math.min(Math.max(rowsRef.current.length, FIRST_CHUNK), WINDOW_MAX));
    const open = detailRef.current;
    if (open) {
      getUser(open.id)
        .then((u) => setDetail((d) => (d && d.id === u.id ? u : d)))
        .catch(() => {});
    }
  }, [load]);

  const showMore = () => {
    const my = seq.current;
    setLoadingMore(true);
    fetchWindow(rowsRef.current.length, FIRST_CHUNK)
      .then((p) => {
        if (my !== seq.current) return;
        setPage(p);
        // Users created or removed since the last chunk shift the offsets; a row that
        // is already on screen is not drawn twice.
        setRows((prev) => {
          const have = new Set(prev.map((u) => u.id));
          return [...prev, ...p.users.filter((u) => !have.has(u.id))];
        });
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoadingMore(false));
  };

  // The card needs the whole user — the links and the subscription — which the list
  // does not carry.
  const openDetail = (id: number) => {
    getUser(id)
      .then(setDetail)
      .catch((e) => notifyError(errMessage(e)));
  };

  const all = page?.all ?? 0
  // What the chips count: everyone until a tag or a group narrows the list.
  const subset = page?.subset ?? all
  const total = page?.total ?? 0;
  const counts = page?.counts ?? {};
  const tagOptions = page?.tags ?? [];
  const rest = Math.max(0, total - rows.length);

  useEffect(() => {
    onTotal(all);
  }, [all, onTotal]);

  // A tag that disappears from every user drops out of the options, and a filter
  // pinned to it falls back to "any".
  useEffect(() => {
    if (!page || tagFilter.length === 0) return
    const live = tagFilter.filter((t) => page.tags.some((o) => o.tag === t))
    if (live.length !== tagFilter.length) setTagFilter(live)
  }, [page, tagFilter]);

  // The groups are the same list the Groups tab edits, read once when the list opens.
  // A role without the groups permission gets a refusal here, which simply means the
  // filter stays hidden — the list itself is none of its business.
  useEffect(() => {
    let alive = true
    listGroups()
      .then((g) => alive && setGroups(g))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, []);
  // A deleted group leaves a choice that matches nobody: it drops out of the selection.
  useEffect(() => {
    if (groups.length === 0 || groupFilter.length === 0) return
    const live = groupFilter.filter((g) => g === "none" || groups.some((x) => String(x.id) === g))
    if (live.length !== groupFilter.length) setGroupFilter(live)
  }, [groups, groupFilter]);

  // Which ids "all matching" means, when it is known: fetched for this filter, or
  // every matching row already on screen.
  const knownIds =
    matchIds && matchIds.key === filterKey
      ? matchIds.ids
      : page && rows.length >= total
        ? rows.map((u) => u.id)
        : null;
  const allFilteredSelected =
    !!knownIds && knownIds.length > 0 && knownIds.every((id) => selected.has(id));

  const toggleOne = (id: number, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const toggleAllFiltered = async () => {
    if (allFilteredSelected && knownIds) {
      setSelected((prev) => {
        const next = new Set(prev);
        for (const id of knownIds) next.delete(id);
        return next;
      });
      return;
    }
    let ids = knownIds;
    if (!ids) {
      const key = filterKey;
      try {
        ids = (
          await listUsersPage({
            q: search,
            filter: filters,
            tag: tagFilter,
            group: groupFilter,
            sort,
            dir: sortDir,
            limit: 0,
            ids: true,
          })
        ).ids ?? [];
      } catch (e) {
        notifyError(errMessage(e));
        return;
      }
      // The filter moved on while the ids were on their way: they answer a question
      // no longer on screen, and selecting them would pick users the operator no
      // longer sees.
      if (key !== filterKeyRef.current) return;
      setMatchIds({ key, ids });
    }
    const add = ids;
    setSelected((prev) => new Set([...prev, ...add]));
  };

  const clearSelection = () => setSelected(new Set());
  // cycleSort walks a column through its natural order, the opposite one, and back to
  // the list's own newest-first order.
  const cycleSort = (id: string) => {
    const natural = sortColumns[id] ?? "asc"
    if (sort !== id) {
      setSort(id)
      setSortDir(natural)
      return
    }
    if (sortDir === natural) {
      setSortDir(natural === "asc" ? "desc" : "asc")
      return
    }
    setSort("new")
    setSortDir("")
  }

  const clearFilters = () => {
    setQuery("");
    setFilters([]);
    setTagFilter([]);
    setGroupFilter([]);
    setSort("new");
    setSortDir("");
  };
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
        onOpenUser={openDetail}
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
  if (all === 0) {
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
      {/* The filter bar: the search, then the saved questions, then the two attributes
          that narrow the same list. The order lives in the table's own header — a click
          on a column — so the bar has nothing to say about it. */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-brand-600/10 bg-gray-50/70 px-5 py-2">
        <span className="flex h-8 w-full shrink-0 items-center gap-2 rounded-lg border border-gray-200 bg-white px-2.5 sm:w-56">
          <IconSearch size={14} className="shrink-0 text-ink-muted" />
          <input
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            placeholder={t("usersPanel.searchPlaceholder")}
            className="min-w-0 flex-1 bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-muted"
          />
        </span>

        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          {/* Statuses: any of the ticked ones. Nothing ticked is everyone, so there is
              no "all" chip — the menu says it instead. */}
          <Dropdown
            align="start"
            width={240}
            trigger={
              <BarButton
                label={t("usersPanel.statusLabel")}
                value={selectionLabel(filters.map((f) => chipLabel(f)))}
                placeholder={t("usersPanel.chipAll")}
              />
            }
          >
            <MenuItem
              checked={filters.length === 0}
              label={t("usersPanel.chipAll")}
              count={subset}
              onClick={() => setFilters([])}
            />
            {CHIPS.filter((c) => (counts[c.value] ?? 0) > 0).map((c) => (
              <MenuItem
                key={c.value}
                checked={filters.includes(c.value)}
                label={t(c.key as "usersPanel.chipActive")}
                count={counts[c.value]}
                onClick={() => setFilters(toggle(filters, c.value))}
              />
            ))}
          </Dropdown>

          {tagOptions.length > 0 && (
            <Dropdown
              align="start"
              width={240}
              trigger={
                <BarButton
                  label={t("usersPanel.tagLabel")}
                  value={selectionLabel(tagFilter)}
                  placeholder={t("usersPanel.anyTag")}
                />
              }
            >
              <MenuItem
                checked={tagFilter.length === 0}
                label={t("usersPanel.anyTag")}
                onClick={() => setTagFilter([])}
              />
              {tagOptions.map((o) => (
                <MenuItem
                  key={o.tag}
                  checked={tagFilter.includes(o.tag)}
                  label={o.tag}
                  count={o.count}
                  onClick={() => setTagFilter(toggle(tagFilter, o.tag))}
                />
              ))}
            </Dropdown>
          )}

          {groups.length > 0 && (
            <Dropdown
              align="start"
              width={240}
              trigger={
                <BarButton
                  label={t("usersPanel.groupLabel")}
                  value={selectionLabel(
                    groupFilter.map((g) =>
                      g === "none"
                        ? t("usersPanel.groupNone")
                        : groups.find((x) => String(x.id) === g)?.name,
                    ),
                  )}
                  placeholder={t("usersPanel.anyGroup")}
                />
              }
            >
              <MenuItem
                checked={groupFilter.length === 0}
                label={t("usersPanel.anyGroup")}
                onClick={() => setGroupFilter([])}
              />
              <MenuItem
                checked={groupFilter.includes("none")}
                label={t("usersPanel.groupNone")}
                onClick={() => setGroupFilter(toggle(groupFilter, "none"))}
              />
              {groups.map((g) => (
                <MenuItem
                  key={g.id}
                  checked={groupFilter.includes(String(g.id))}
                  label={g.name}
                  onClick={() => setGroupFilter(toggle(groupFilter, String(g.id)))}
                />
              ))}
            </Dropdown>
          )}

          {(search || filters.length > 0 || tagFilter.length > 0 || groupFilter.length > 0 || sort !== "new") && (
            <button
              type="button"
              onClick={clearFilters}
              className="inline-flex h-8 shrink-0 items-center gap-1 rounded-lg border border-gray-200 bg-white px-2.5 text-xs text-ink-muted transition hover:border-gray-300 hover:text-ink"
            >
              <IconClose size={12} className="shrink-0 opacity-70" />
              {t("usersPanel.resetFilters")}
            </button>
          )}
        </div>
      </div>

      <div ref={listRef} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {total === 0 ? (
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
              label={t("usersPanel.selectAll", { count: total })}
            />
            <SortHead
              id="name"
              label={t("usersPanel.colUser")}
              sort={sort}
              dir={sortDir}
              onSort={cycleSort}
            />
            <span className="truncate">{t("usersPanel.colStatus")}</span>
            <SortHead
              id="traffic"
              label={t("usersPanel.colTraffic")}
              sort={sort}
              dir={sortDir}
              onSort={cycleSort}
            />
            <SortHead
              id="expiry"
              label={t("usersPanel.colExpires")}
              sort={sort}
              dir={sortDir}
              onSort={cycleSort}
            />
            {roomy && (
              <>
                <span className="truncate">{t("usersPanel.colDevices")}</span>
                <span className="truncate">{t("usersPanel.colGroups")}</span>
              </>
            )}
          </div>
          )}
          {rows.map((u) => (
            <UserRow
              key={u.id}
              u={u}
              wide={wide}
              roomy={roomy}
              checked={selected.has(u.id)}
              onToggle={(v) => toggleOne(u.id, v)}
              onDetail={() => openDetail(u.id)}
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
              {canManage && (
                <>
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
                </>
              )}
              <PluginActionButtons
                scope="users"
                userIds={[...selected]}
                disabled={pending !== null || selected.size > 1000}
                onDone={refresh}
              />
              {canDelete && (
                <Button size="xs" variant="outline" color="red" disabled={pending !== null} onClick={() => setConfirmDelete(true)}>
                  {t("common.delete")}
                </Button>
              )}
              <button
                type="button"
                onClick={clearSelection}
                disabled={pending !== null}
                className="text-xs font-medium text-accent hover:underline disabled:opacity-60"
              >
                {t("usersPanel.clearSelection")}
              </button>
              {rest > 0 && (
                <span className="ms-auto text-xs text-ink-muted">
                  {t("usersPanel.shownOf", { shown: rows.length, total })}
                </span>
              )}
            </div>
          ) : (
            <div className="flex items-center gap-3">
              {rest > 0 && (
                <span className="text-xs text-ink-muted">
                  {t("usersPanel.shownOf", { shown: rows.length, total })}
                </span>
              )}
              {rest > 0 && (
                <Button
                  size="xs"
                  variant="outline"
                  color="gray"
                  className="ms-auto"
                  loading={loadingMore}
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
            ? "border-brand-600 bg-brand-600 text-onbrand"
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
function TrafficCell({ u }: { u: UserRow }) {
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
  onDetail,
}: {
  u: UserRow;
  wide: boolean;
  roomy: boolean;
  checked: boolean;
  onToggle: (v: boolean) => void;
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
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500/25",
        checked
          ? "accent-tint"
          : limited
            ? "warning-tint"
            : "hover:bg-gray-50",
      )}
      style={{ gridTemplateColumns: wide ? (roomy ? TPL : TPL_MID) : TPL_MOBILE }}
    >
      {/* Selecting is not opening: the box swallows the click that would open the card. */}
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: not a control — it only keeps
          the checkbox's click from reaching the row behind it; the checkbox itself is
          focusable and answers Space. */}
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
        <Mono
          className="truncate text-[11px] text-ink-muted"
          title={u.expire_at === 0 && u.hold_seconds > 0 ? fmtTerm(0, u.hold_seconds) : undefined}
        >
          {u.expire_at > 0
            ? fmtExpire(u.expire_at)
            : u.hold_seconds > 0
              ? t("usersPanel.holdShort", { count: Math.floor(u.hold_seconds / 86400) })
              : "—"}
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
  // The term is a date, or a number of days that starts on the first connection.
  const [termMode, setTermMode] = useState("date");
  const [holdDays, setHoldDays] = useState("30");
  // "0" is manual — the limits below. Any other value is a tariff, which owns them.
  const [plan, setPlan] = useState("0");
  const [plans, setPlans] = useState<TariffPlan[]>([]);
  const [billingOn, setBillingOn] = useState(false);
  const [created, setCreated] = useState<User | null>(null);
  const { t } = useTranslation();
  const { busy, run } = useAction();
  const { copied, copy } = useCopy();

  // The tariff list is read when the dialog opens, not with the page: most sessions
  // never open it, and an install with billing off has nothing to show here at all.
  useEffect(() => {
    if (!opened) return;
    let alive = true;
    getBilling()
      .then((b) => {
        if (!alive) return;
        setBillingOn(!!b.enabled);
        setPlans((b.plans ?? []).filter((p) => p.enabled));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [opened]);

  const onPlan = plan !== "0";
  const held = !onPlan && termMode === "hold";
  const holdN = Math.floor(Number(holdDays) || 0);

  const submit = async () => {
    if (!name.trim()) return;
    run(async () => {
      // Under a tariff the account is created bare: applying the plan writes the
      // quota, the device cap and the reset cycle itself (planWriteFor), so sending
      // hand-set limits first would only be overwritten a moment later.
      const dl = onPlan ? 0 : gbToBytes(Number(limitGb) || 0);
      const ea = onPlan || held ? 0 : dateToUnixEndOfDay(expDate);
      const u = await createUser(name.trim(), dl, ea, held ? holdN * 86400 : 0);
      if (onPlan) await setUserPlan(u.id, Number(plan));
      else if (resetPeriod !== "none") await setResetPeriod(u.id, resetPeriod);
      setCreated(u);
    });
  };

  const close = () => {
    setName("");
    setLimitGb("0");
    setResetPeriodState("none");
    setExpDate("");
    setTermMode("date");
    setHoldDays("30");
    setPlan("0");
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
          {/* A tariff instead of the three fields below it: it carries the expiry,
              the quota and the reset cycle, so showing them under one would offer
              edits the plan overwrites on the next line of the same save. */}
          {billingOn && plans.length > 0 && (
            <Select
              label={t("userDetail.plan")}
              data={[
                { value: "0", label: t("userDetail.manual") },
                ...plans.map((p) => ({ value: String(p.id), label: p.name })),
              ]}
              value={plan}
              onChange={setPlan}
            />
          )}
          {onPlan ? (
            <p className="text-xs text-ink-muted">{t("usersPanel.planSetsLimits")}</p>
          ) : (
            <>
              <Select
                label={t("usersPanel.termMode")}
                data={termModes()}
                value={termMode}
                onChange={setTermMode}
              />
              <div className="grid grid-cols-2 gap-3">
                {held ? (
                  <TextInput
                    label={t("usersPanel.holdDays")}
                    type="number"
                    value={holdDays}
                    onChange={(v) => setHoldDays(v.replace(/\D/g, ""))}
                  />
                ) : (
                  <DatePicker
                    label={t("usersPanel.validUntil")}
                    value={expDate}
                    onChange={setExpDate}
                    min={unixToLocalDate(Math.floor(Date.now() / 1000))}
                  />
                )}
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
            </>
          )}
          <Button loading={busy} disabled={held && holdN < 1} onClick={submit}>
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

// tagCls is the tag label in a list row: smaller than a Badge, because the row already
// carries a name, an id and a status and the tags are the quietest thing on it.
const tagCls = "shrink-0 rounded bg-gray-100 px-1.5 py-0 text-[10px] leading-4 text-gray-600"

// TagList renders a user's tags as small muted badges — muted so they read as labels
// the operator attached, not as a status the panel derived.
function TagList({ tags, max = tagsShown }: { tags: string[]; max?: number }) {
  if (tags.length === 0) return null;
  const shown = tags.slice(0, max);
  const rest = tags.slice(max);
  return (
    <>
      {shown.map((tag) => (
        <span key={tag} className={tagCls}>
          {tag}
        </span>
      ))}
      {rest.length > 0 && (
        <span className={tagCls} title={rest.join(", ")}>
          +{rest.length}
        </span>
      )}
    </>
  );
}
