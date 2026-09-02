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
import { useAction, useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  Checkbox,
  Modal,
  ShowMore,
  TextInput,
} from "./ui";

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
  const [editing, setEditing] = useState<{
    id: number;
    name: string;
    grants: Set<string>;
    members: Set<number>;
  } | null>(null);
  const [confirmDel, setConfirmDel] = useState<Group | null>(null);
  const { busy, run } = useAction();

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

  return (
    <div className="flex flex-col gap-3">
      {/* White, not the gray-50 tint used elsewhere: this tab renders straight onto the
          page background, where that tint has nothing to sit against and the blocks
          read as empty space. The tinted variant belongs inside a card or a modal. */}
      <div className="rounded-xl border border-gray-200/80 bg-white p-4">
        <h3 className="mb-1 font-bold text-ink">{t("groups.title")}</h3>
        <p className="text-sm text-ink-muted">{t("groups.description")}</p>
      </div>

      {groups.length === 0 && (
        <p className="px-1 text-sm text-ink-muted">
          {t("groups.empty")}
        </p>
      )}

      <div className="flex flex-col gap-2">
        {groups.map((g) => (
          <div
            key={g.id}
            className="flex items-center justify-between gap-3 rounded-xl border border-gray-200/80 bg-white p-4"
          >
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="font-medium text-ink">{g.name}</span>
              <Badge color="gray">
                {t("groups.nConnections", { count: g.grants?.length ?? 0 })}
              </Badge>
              <Badge color="gray">
                {t("groups.nMembers", { count: g.members })}
              </Badge>
            </div>
            <div className="flex shrink-0 gap-2">
              <Button
                size="sm"
                variant="light"
                color="gray"
                onClick={() =>
                  setEditing({
                    id: g.id,
                    name: g.name,
                    grants: new Set(g.grants ?? []),
                    members: new Set(g.member_ids ?? []),
                  })
                }
              >
                {t("common.edit")}
              </Button>
              <Button size="sm" variant="light" color="red" onClick={() => setConfirmDel(g)}>
                {t("common.delete")}
              </Button>
            </div>
          </div>
        ))}
      </div>

      <div>
        <Button
          variant="light"
          onClick={() => setEditing({ id: 0, name: "", grants: new Set(), members: new Set() })}
        >
          {t("groups.create")}
        </Button>
      </div>

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing?.id ? t("groups.group") : t("groups.newGroup")}
        size="lg"
      >
        {editing && (
          <div className="flex flex-col gap-4">
            <TextInput
              label={t("groups.name")}
              value={editing.name}
              onChange={(v) => setEditing({ ...editing, name: v })}
              placeholder={t("groups.namePlaceholder")}
            />
            <div className="flex flex-col gap-2">
              <div className="flex items-center justify-between gap-3">
                <p className="text-sm text-ink-muted">{t("groups.grantsIntro")}</p>
                <Badge color={editing.grants.size > 0 ? "brand" : "gray"}>
                  {t("groups.nConnections", { count: editing.grants.size })}
                </Badge>
              </div>
              {/* The list grows with every server and every external subscription;
                  it scrolls inside the dialog so the members and the buttons below
                  stay in reach. */}
              <div className="flex max-h-72 flex-col gap-3 overflow-y-auto pr-1">
                {targets.map((srv) => (
                  <div key={srv.server_id} className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-3">
                    <p className="mb-2 text-sm font-semibold text-ink">{srv.server_name}</p>
                    <div className="flex flex-col gap-1.5">
                      {srv.lanes.map((l) => (
                        <GrantRow
                          key={l.token}
                          token={l.token}
                          label={LANE_LABELS[l.lane] ?? l.label}
                          off={!l.enabled}
                          grants={editing.grants}
                          onToggle={(g) => setEditing({ ...editing, grants: g })}
                        />
                      ))}
                      {srv.inbounds.map((i) => (
                        <GrantRow
                          key={i.token}
                          token={i.token}
                          label={i.name}
                          badge={t("groups.extraBadge")}
                          off={!i.enabled}
                          grants={editing.grants}
                          onToggle={(g) => setEditing({ ...editing, grants: g })}
                        />
                      ))}
                      {srv.external?.map((e) => (
                        <GrantRow
                          key={e.token}
                          token={e.token}
                          label={e.name}
                          badge={t("groups.externalBadge")}
                          off={!e.enabled}
                          grants={editing.grants}
                          onToggle={(g) => setEditing({ ...editing, grants: g })}
                        />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            <MembersPicker
              users={users}
              members={editing.members}
              onChange={(m) => setEditing({ ...editing, members: m })}
            />

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
function MembersPicker({
  users,
  members,
  onChange,
}: {
  users: User[];
  members: Set<number>;
  onChange: (m: Set<number>) => void;
}) {
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
  }, [users, query, members]);

  // Every user on the install lands here, so on a big panel this one picker used to
  // mount thousands of checkboxes inside a 14rem scroll box. Selected members sort
  // first, which is what makes a short first chunk usable: the current set is in it.
  // A new search starts from the top again.
  const page = useShowMore(filtered, { resetKey: query });

  const toggle = (id: number) => {
    const next = new Set(members);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(next);
  };

  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-2 border-t border-gray-100 pt-3">
      <div className="flex items-center justify-between">
        <span className="text-sm text-ink-muted">{t("groups.members")}</span>
        <Badge color="gray">{t("groups.nSelected", { count: members.size })}</Badge>
      </div>
      <TextInput value={query} onChange={setQuery} placeholder={t("groups.searchUsers")} />
      {users.length === 0 ? (
        <p className="text-xs text-ink-muted">{t("groups.noUsers")}</p>
      ) : (
        <div className="max-h-56 overflow-y-auto rounded-xl border border-gray-200/80 bg-gray-50/60 p-2">
          <div className="flex flex-col gap-1.5">
            {page.shown.map((u) => (
              <Checkbox
                key={u.id}
                checked={members.has(u.id)}
                onChange={() => toggle(u.id)}
                label={u.name}
                hint={u.system_email}
              />
            ))}
            <ShowMore rest={page.rest} onClick={page.showMore} className="mt-1" />
            {filtered.length === 0 && (
              <p className="px-1 py-2 text-xs text-ink-muted">{t("common.nothingFound")}</p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// GrantRow is one grantable connection: a checkbox that adds/removes its token.
function GrantRow({
  token,
  label,
  badge,
  off,
  grants,
  onToggle,
}: {
  token: string;
  label: string;
  badge?: string;
  off?: boolean;
  grants: Set<string>;
  onToggle: (g: Set<string>) => void;
}) {
  const { t } = useTranslation();
  return (
    <Checkbox
      checked={grants.has(token)}
      onChange={(c) => {
        const next = new Set(grants);
        if (c) next.add(token);
        else next.delete(token);
        onToggle(next);
      }}
      label={
        <span className="flex items-center gap-2">
          <span>{label}</span>
          {badge && <Badge color="gray">{badge}</Badge>}
          {off && <Badge color="gray">{t("groups.off")}</Badge>}
        </span>
      }
    />
  );
}
