import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createGroup,
  deleteGroup,
  getGroupTargets,
  listGroups,
  listUsers,
  setGroupMembers,
  updateGroup,
  type Group,
  type GroupTarget,
  type User,
} from "./api";
import { statusInfo } from "./format";
import { useAction, useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  EmptyState,
  IconButton,
  IconCheck,
  IconPencil,
  IconPlus,
  IconTrash,
  MICRO,
  Modal,
  Mono,
  Panel,
  ShowMore,
  TextInput,
  useWideBox,
} from "./ui";

// One template for the header and every row; narrow, the row folds to name + actions
// with the figures on a second line.
// Every track is minmax(0,…): a bare `1fr` floors at the cell's min-content, so one
// long name would widen that row's column and the rows would stop lining up. The
// action track is a fixed width for the same reason — the header's is empty, and an
// `auto` track would resolve to zero there and to two buttons in every row.
const TPL = "minmax(0,1.6fr) minmax(0,1fr) minmax(0,1fr) 76px";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 520;

// The dialog's draft: what is being edited before it is saved.
interface Editing {
  id: number;
  name: string;
  grants: Set<string>;
  members: Set<number>;
}

const LANE_LABELS: Record<string, string> = {
  vless: "VLESS-Vision",
  reality: "VLESS-XHTTP-REALITY",
  hysteria2: "Hysteria2",
};

// GroupsPanel manages user groups: each group is a named set of connections its
// members may reach. A user in no group reaches everything; membership is assigned
// on the user (in the user drawer), not here.
export function GroupsPanel() {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [targets, setTargets] = useState<GroupTarget[] | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [confirmDel, setConfirmDel] = useState<Group | null>(null);
  const { busy, run } = useAction();
  const [boxRef, wide] = useWideBox(WIDE_MIN);
  const [tab, setTab] = useState<"grants" | "members">("grants");

  const reload = () => listGroups().then(setGroups);

  useEffect(() => {
    Promise.all([listGroups(), getGroupTargets(), listUsers()])
      .then(([g, t, u]) => {
        setGroups(g);
        setTargets(t);
        setUsers(u);
      })
      .catch((e) => {
        notifyError(errMessage(e));
        setGroups([]);
      });
  }, []);

  const save = () => {
    if (!editing) return;
    const { id, name, grants, members } = editing;
    run(async () => {
      const list = [...grants];
      // A new group must exist before it can hold members, so create first then set
      // membership; an edit sets both against the known id.
      const gid = id === 0 ? (await createGroup(name, list)).id : id;
      if (id !== 0) await updateGroup(id, name, list);
      await setGroupMembers(gid, [...members]);
      await reload();
      setEditing(null);
      notifySuccess(t("common.saved"));
    });
  };

  const remove = (g: Group) =>
    run(async () => {
      await deleteGroup(g.id);
      await reload();
      setConfirmDel(null);
      notifySuccess(t("groups.deleted"));
    });

  if (!groups || !targets) return <CenterLoader />;


  // Icon, not words: it is the one action of the section header, and it reads the
  // same as "add a user" two tabs over. The title carries the label.
  const openEditor = (e: Editing) => {
    setTab("grants");
    setEditing(e);
  };

  const createBtn = (
    <IconButton
      variant="filled"
      color="brand"
      title={t("groups.create")}
      onClick={() =>
        openEditor({ id: 0, name: "", grants: new Set(), members: new Set() })
      }
    >
      <IconPlus />
    </IconButton>
  );

  return (
    <div className="flex flex-col gap-3.5">
      <Panel title={t("groups.title")} aside={createBtn}>
        {groups.length === 0 ? (
          <EmptyState title={t("groups.empty")} />
        ) : (
          <div ref={boxRef}>
            {wide && (
              <div
                className={cn(MICRO, "grid items-center gap-3 border-t border-brand-600/10 px-3.5 py-2")}
                style={{ gridTemplateColumns: TPL }}
              >
                <span className="truncate">{t("groups.colName")}</span>
                <span className="truncate">{t("groups.colConnections")}</span>
                <span className="truncate">{t("groups.colMembers")}</span>
                <span />
              </div>
            )}
            {groups.map((g) => {
              const grants = g.grants ?? [];
              return (
                <div
                  key={g.id}
                  className="grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]"
                  style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
                >
                  <span className="truncate text-[13px] font-medium text-ink" title={g.name}>
                    {g.name}
                  </span>
                  {wide ? (
                    <>
                      <Mono className="text-xs text-ink-muted">{grants.length}</Mono>
                      <Mono className="text-xs text-ink-muted">{g.members}</Mono>
                    </>
                  ) : (
                    <span className="col-start-1 row-start-2 truncate text-[11px] text-ink-muted">
                      {t("groups.nConnections", { count: grants.length })} ·{" "}
                      {t("groups.nMembers", { count: g.members })}
                    </span>
                  )}
                  {/* Icons, like the roster two tabs over: the row is read for its
                      name, not for the two words repeated down every line. */}
                  <span
                    className={cn(
                      "flex justify-end gap-0.5",
                      !wide && "col-start-2 row-span-2 row-start-1",
                    )}
                  >
                    <IconButton
                      title={t("common.edit")}
                      onClick={() =>
                        openEditor({
                          id: g.id,
                          name: g.name,
                          grants: new Set(g.grants ?? []),
                          members: new Set(g.member_ids ?? []),
                        })
                      }
                    >
                      <IconPencil size={16} />
                    </IconButton>
                    <IconButton
                      color="red"
                      title={t("common.delete")}
                      onClick={() => setConfirmDel(g)}
                    >
                      <IconTrash size={16} />
                    </IconButton>
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </Panel>

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing?.id ? t("groups.group") : t("groups.newGroup")}
        size="lg"
      >
        {editing && (
          <div className="flex flex-col gap-3.5">
            <TextInput
              label={t("groups.name")}
              value={editing.name}
              onChange={(v) => setEditing({ ...editing, name: v })}
              placeholder={t("groups.namePlaceholder")}
            />

            {/* Two lists, one dialog: what the group hands out, and who is in it.
                Tabs rather than one long scroll — the buttons at the bottom were
                pushed out of reach by whichever list happened to be long. */}
            <div className="-mx-5 flex gap-0.5 border-b border-brand-600/10 px-5">
              {(
                [
                  ["grants", t("groups.tabGrants"), editing.grants.size],
                  ["members", t("groups.tabMembers"), editing.members.size],
                ] as const
              ).map(([value, label, count]) => (
                <button
                  key={value}
                  type="button"
                  onClick={() => setTab(value)}
                  className={cn(
                    "flex items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-[13px] font-semibold transition",
                    tab === value
                      ? "border-brand-600 text-ink"
                      : "border-transparent text-ink-muted hover:text-ink",
                  )}
                >
                  {label}
                  <Mono className="text-[11px] text-ink-muted">{count}</Mono>
                </button>
              ))}
            </div>

            {tab === "grants" ? (
              <GrantsTable
                targets={targets}
                grants={editing.grants}
                onChange={(g) => setEditing({ ...editing, grants: g })}
              />
            ) : (
              <MembersTable
                users={users}
                members={editing.members}
                onChange={(m) => setEditing({ ...editing, members: m })}
              />
            )}

            <div className="flex justify-end gap-2 border-t border-gray-100 pt-3">
              <Button variant="light" color="gray" onClick={() => setEditing(null)} disabled={busy}>
                {t("common.cancel")}
              </Button>
              <Button onClick={save} loading={busy} disabled={!editing.name.trim()}>
                {t("common.save")}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      <Modal open={!!confirmDel} onClose={() => setConfirmDel(null)} title={t("groups.deleteTitle")}>
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            {t("groups.deleteBody", { name: confirmDel?.name ?? "" })}
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setConfirmDel(null)}>
              {t("common.cancel")}
            </Button>
            <Button color="red" loading={busy} onClick={() => confirmDel && remove(confirmDel)}>
              {t("common.delete")}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

// MembersPicker is the group-side membership editor: a searchable, checkable user
// list. Membership is also editable per user (the user drawer); this is the same
// relation seen from the group.
// Both dialog tables share a shape: a checkbox column, the thing's name, and what
// else is worth knowing about it. Dense rows with dividers, a micro header, and the
// body scrolls inside the dialog so the save buttons stay in reach.
const GRANT_TPL = "28px minmax(0,1.7fr) minmax(0,1fr) minmax(0,.7fr)";
// How deep both dialog tables open, and how much a "show more" adds. The same 50 the
// users list uses: enough to scan, short enough to render instantly on a big install.
const PAGE = 50;
const MEMBER_TPL = "28px minmax(0,1.6fr) minmax(0,1fr) minmax(0,.8fr)";

function CheckCell({
  checked,
  mixed,
  onChange,
  label,
}: {
  checked: boolean;
  // Some but not all of what this box stands for — the header box over a partly
  // selected list. It reads as a dash, and clicking it selects the rest.
  mixed?: boolean;
  onChange: (v: boolean) => void;
  label: string;
}) {
  const on = checked || mixed;
  return (
    <label className="relative flex shrink-0 cursor-pointer items-center">
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
          on
            ? "border-brand-600 bg-brand-600 text-onaccent"
            : "border-gray-300 bg-white",
        )}
      >
        {mixed ? (
          <span className="h-0.5 w-2 rounded-full bg-current" />
        ) : (
          checked && <IconCheck size={12} />
        )}
      </span>
    </label>
  );
}

// GrantsTable is every connection the panel can hand out, one row each: which server
// it belongs to and whether it is switched on there.
function GrantsTable({
  targets,
  grants,
  onChange,
}: {
  targets: GroupTarget[];
  grants: Set<string>;
  onChange: (g: Set<string>) => void;
}) {
  const { t } = useTranslation();
  const rows = targets.flatMap((srv) => [
    ...srv.lanes.map((l) => ({
      token: l.token,
      name: LANE_LABELS[l.lane] ?? l.label,
      kind: "",
      server: srv.server_name,
      off: !l.enabled,
    })),
    ...srv.inbounds.map((i) => ({
      token: i.token,
      name: i.name,
      kind: t("groups.extraBadge"),
      server: srv.server_name,
      off: !i.enabled,
    })),
    ...(srv.external ?? []).map((e) => ({
      token: e.token,
      name: e.name,
      kind: t("groups.externalBadge"),
      server: srv.server_name,
      off: !e.enabled,
    })),
  ]);

  // A big fleet with custom inbounds and external subscriptions runs to hundreds of
  // rows; they arrive a page at a time like every other list in the panel.
  const page = useShowMore(rows, { first: PAGE, step: PAGE });

  const toggle = (token: string, on: boolean) => {
    const next = new Set(grants);
    if (on) next.add(token);
    else next.delete(token);
    onChange(next);
  };

  const picked = rows.filter((r) => grants.has(r.token)).length;
  const allPicked = rows.length > 0 && picked === rows.length;
  // Everything, or nothing: a half-selected list is completed rather than cleared.
  const toggleAll = () =>
    onChange(allPicked ? new Set() : new Set(rows.map((r) => r.token)));

  return (
    <div className="-mx-5 flex flex-col">
      <div
        className={cn(MICRO, "grid items-center gap-3 border-b border-gray-100 px-5 py-2")}
        style={{ gridTemplateColumns: GRANT_TPL }}
      >
        <CheckCell
          checked={allPicked}
          mixed={picked > 0 && !allPicked}
          onChange={toggleAll}
          label={t("usersPanel.selectAll", { count: rows.length })}
        />
        <span className="truncate">{t("groups.colConn")}</span>
        <span className="truncate">{t("groups.colServer")}</span>
        <span className="truncate text-right">{t("groups.colState")}</span>
      </div>
      <div className="max-h-[46vh] overflow-y-auto">
        {rows.length === 0 ? (
          <EmptyState title={t("groups.noConnections")} />
        ) : (
          page.shown.map((r) => {
            const on = grants.has(r.token);
            return (
              <label
                key={r.token}
                className={cn(
                  "grid cursor-pointer items-center gap-3 border-b border-gray-100 px-5 py-[7px] transition last:border-0",
                  on ? "accent-tint" : "hover:bg-gray-50",
                )}
                style={{ gridTemplateColumns: GRANT_TPL }}
              >
                <CheckCell checked={on} onChange={(v) => toggle(r.token, v)} label={r.name} />
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-[13px] text-ink" title={r.name}>
                    {r.name}
                  </span>
                  {r.kind && (
                    <Badge color="gray" size="xs">
                      {r.kind}
                    </Badge>
                  )}
                </span>
                <span className="truncate text-xs text-ink-muted" title={r.server}>
                  {r.server}
                </span>
                <span
                  className={cn(
                    "truncate text-right text-xs",
                    r.off ? "text-warning" : "text-ink-muted",
                  )}
                >
                  {r.off ? t("groups.off") : t("groups.on")}
                </span>
              </label>
            );
          })
        )}
        <ShowMore rest={page.rest} onClick={page.showMore} className="p-3.5" />
      </div>
    </div>
  );
}

// MembersTable is who is in the group. Every user on the install lands here, so the
// current members sort first and the rest arrive a page at a time.
function MembersTable({
  users,
  members,
  onChange,
}: {
  users: User[];
  members: Set<number>;
  onChange: (m: Set<number>) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = q
      ? users.filter(
          (u) =>
            u.name.toLowerCase().includes(q) ||
            u.system_email.toLowerCase().includes(q),
        )
      : users;
    // Selected members first, so the current set is visible without scrolling.
    return [...list].sort((a, b) => {
      const am = members.has(a.id) ? 0 : 1;
      const bm = members.has(b.id) ? 0 : 1;
      return am - bm || a.name.localeCompare(b.name);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [users, query, members]);

  // A new search starts from the top again.
  const page = useShowMore(filtered, { first: PAGE, step: PAGE, resetKey: query });

  const toggle = (id: number, on: boolean) => {
    const next = new Set(members);
    if (on) next.add(id);
    else next.delete(id);
    onChange(next);
  };

  // Over what the search is showing, not over every account on the install: the box
  // must mean the list under it.
  const picked = filtered.filter((u) => members.has(u.id)).length;
  const allPicked = filtered.length > 0 && picked === filtered.length;
  const toggleAll = () => {
    const next = new Set(members);
    for (const u of filtered) {
      if (allPicked) next.delete(u.id);
      else next.add(u.id);
    }
    onChange(next);
  };

  return (
    <div className="flex flex-col gap-2.5">
      <TextInput
        value={query}
        onChange={setQuery}
        placeholder={t("groups.searchUsers")}
      />
      <div className="-mx-5 flex flex-col">
        <div
          className={cn(MICRO, "grid items-center gap-3 border-b border-gray-100 px-5 py-2")}
          style={{ gridTemplateColumns: MEMBER_TPL }}
        >
          <CheckCell
            checked={allPicked}
            mixed={picked > 0 && !allPicked}
            onChange={toggleAll}
            label={t("usersPanel.selectAll", { count: filtered.length })}
          />
          <span className="truncate">{t("groups.colUser")}</span>
          <span className="truncate">{t("groups.colId")}</span>
          <span className="truncate text-right">{t("groups.colStatus")}</span>
        </div>
        <div className="max-h-[38vh] overflow-y-auto">
          {users.length === 0 ? (
            <EmptyState title={t("groups.noUsers")} />
          ) : filtered.length === 0 ? (
            <EmptyState title={t("common.nothingFound")} />
          ) : (
            <>
              {page.shown.map((u) => {
                const on = members.has(u.id);
                const st = statusInfo(u.status);
                return (
                  <label
                    key={u.id}
                    className={cn(
                      "grid cursor-pointer items-center gap-3 border-b border-gray-100 px-5 py-[7px] transition last:border-0",
                      on ? "accent-tint" : "hover:bg-gray-50",
                    )}
                    style={{ gridTemplateColumns: MEMBER_TPL }}
                  >
                    <CheckCell checked={on} onChange={(v) => toggle(u.id, v)} label={u.name} />
                    <span className="truncate text-[13px] text-ink" title={u.name}>
                      {u.name}
                    </span>
                    <Mono className="truncate text-[11px] text-ink-muted">
                      {u.system_email}
                    </Mono>
                    <span
                      className={cn(
                        "truncate text-right text-xs",
                        u.status === "active" ? "text-success" : "text-ink-muted",
                      )}
                    >
                      {st.label}
                    </span>
                  </label>
                );
              })}
              <ShowMore rest={page.rest} onClick={page.showMore} className="p-3.5" />
            </>
          )}
        </div>
      </div>
    </div>
  );
}
