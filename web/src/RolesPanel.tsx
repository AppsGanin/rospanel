import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type AdminRole,
  adminRoleName,
  createRole,
  deleteRole,
  type Perm,
  type PermSection,
  updateRole,
} from "./api";
import { td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useStepUpDialog } from "./stepup";
import {
  Button,
  cn,
  IconButton,
  IconCheck,
  IconPencil,
  IconPlus,
  IconTrash,
  MICRO,
  Modal,
  Panel,
  TextInput,
} from "./ui";

// The roles: what each admin may see and do. The owner's alone, like the roster
// above it. The permission rows come from the server's catalog (GET /api/roles), so
// a permission added there reaches this editor without a second list to keep in step.

const permLabel = (key: string) => td(`permSection.${key}`);

// The editor's grid: section, then a "view" and a "change" column. A permission of
// its own sits in the column it reads as — the journal and the logs are things you
// look at, a backup or an update is something you do.
const GRID = "minmax(0,1fr) 72px 72px";

export function RolesPanel({
  roles,
  catalog,
  implies,
  onChanged,
}: {
  roles: AdminRole[];
  catalog: PermSection[];
  implies: Partial<Record<Perm, Perm[]>>;
  onChanged: () => Promise<unknown> | void;
}) {
  const { t } = useTranslation();
  const { ask, stepUpNode } = useStepUpDialog();
  // The role being edited; key "" is a new one.
  const [editing, setEditing] = useState<{ key: string; preset: boolean } | null>(null);
  const [name, setName] = useState("");
  const [perms, setPerms] = useState<Set<Perm>>(new Set());
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState<AdminRole | null>(null);

  const total = catalog.reduce((n, s) => n + (s.view ? 1 : 0) + (s.manage ? 1 : 0), 0);

  const openNew = () => {
    setEditing({ key: "", preset: false });
    setName("");
    setPerms(new Set());
  };
  const openEdit = (r: AdminRole) => {
    setEditing({ key: r.key, preset: r.preset });
    setName(r.name);
    setPerms(new Set(r.perms));
  };

  // closure is a permission with everything it brings (manage → view, the bots →
  // what the admin bot reaches…), the same expansion the server stores.
  const closure = (p: Perm, into = new Set<Perm>()): Set<Perm> => {
    if (into.has(p)) return into;
    into.add(p);
    for (const q of implies[p] ?? []) closure(q, into);
    return into;
  };

  // Ticking a box ticks what it brings; unticking one unticks everything that would
  // bring it back — so the grid never shows a set the server would not store.
  const toggle = (s: PermSection, which: "view" | "manage", on: boolean) => {
    const p = s[which];
    if (!p) return;
    const next = new Set(perms);
    if (on) {
      for (const q of closure(p)) next.add(q);
    } else {
      for (const q of [...next]) if (closure(q).has(p)) next.delete(q);
    }
    setPerms(next);
  };

  const shown = (key: string, n: string) => adminRoleName({ key, name: n });

  const save = async () => {
    if (!editing) return;
    const label = shown(editing.key, name.trim()) || name.trim();
    const creds = await ask({
      title: editing.key ? t("rolesPanel.editOf", { name: label }) : t("rolesPanel.add"),
      body: editing.key
        ? t("rolesPanel.stepUpSave", { name: label })
        : t("rolesPanel.stepUpCreate", { name: name.trim() }),
      confirmLabel: t("common.save"),
    });
    if (!creds) return;
    setBusy(true);
    try {
      const list = [...perms];
      const r = editing.key
        ? await updateRole(editing.key, name.trim(), list, creds.password)
        : await createRole(name.trim(), list, creds.password);
      notifySuccess(t("rolesPanel.saved", { name: adminRoleName(r) }));
      setEditing(null);
      await onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    const label = adminRoleName(deleting);
    const creds = await ask({
      title: t("rolesPanel.deleteOf", { name: label }),
      body: t("rolesPanel.deleteHint"),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!creds) return;
    setBusy(true);
    try {
      await deleteRole(deleting.key, creds.password);
      notifySuccess(t("rolesPanel.deleted", { name: label }));
      setDeleting(null);
      await onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Panel
        title={t("rolesPanel.title")}
        aside={
          <IconButton variant="filled" color="brand" title={t("rolesPanel.add")} onClick={openNew}>
            <IconPlus />
          </IconButton>
        }
      >
        {roles.map((r) => {
          const held = r.admins + r.api_keys > 0;
          return (
            <div
              key={r.key}
              className="flex items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
            >
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-xs font-medium text-ink">
                    {adminRoleName(r)}
                  </span>
                  {r.preset && (
                    <span className="shrink-0 text-[11px] text-ink-muted">
                      {t("rolesPanel.preset")}
                    </span>
                  )}
                </div>
                <p className="truncate text-[11px] text-ink-muted">
                  {t("rolesPanel.permsCount", { count: r.perms.length, total })}
                  {" · "}
                  {t("rolesPanel.holders", { admins: r.admins, keys: r.api_keys })}
                </p>
              </div>
              <span className="flex shrink-0 gap-0.5">
                <IconButton title={t("common.edit")} onClick={() => openEdit(r)}>
                  <IconPencil size={16} />
                </IconButton>
                {/* A preset stays, and a role still held has nowhere for its holders
                    to go — the server refuses both, so neither gets the button. */}
                {!r.preset && !held && (
                  <IconButton color="red" title={t("common.delete")} onClick={() => setDeleting(r)}>
                    <IconTrash size={16} />
                  </IconButton>
                )}
              </span>
            </div>
          );
        })}
      </Panel>

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        size="lg"
        title={
          editing?.key
            ? t("rolesPanel.editOf", { name: shown(editing.key, name) })
            : t("rolesPanel.add")
        }
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setEditing(null)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {t("common.save")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("rolesPanel.name")}
            value={name}
            onChange={setName}
            placeholder={
              editing?.preset ? t("rolesPanel.presetName") : t("rolesPanel.namePlaceholder")
            }
            autoFocus={!editing?.key}
          />
          <div className="overflow-hidden rounded-xl border border-gray-200">
            <div
              className={cn(MICRO, "grid items-center gap-3 bg-gray-50 px-3.5 py-2")}
              style={{ gridTemplateColumns: GRID }}
            >
              <span>{t("rolesPanel.colSection")}</span>
              <span className="text-center">{t("rolesPanel.colView")}</span>
              <span className="text-center">{t("rolesPanel.colManage")}</span>
            </div>
            {catalog.map((s) => {
              // A single permission that reads as looking (stats, logs, the journal)
              // has only a view; one that reads as doing has only a manage.
              return (
                <div
                  key={s.key}
                  className="grid items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
                  style={{ gridTemplateColumns: GRID }}
                >
                  <span className="min-w-0 text-xs text-ink">{permLabel(s.key)}</span>
                  <span className="flex justify-center">
                    {s.view && (
                      <PermCheck
                        checked={perms.has(s.view)}
                        onChange={(v) => toggle(s, "view", v)}
                        label={`${permLabel(s.key)}: ${t("rolesPanel.colView")}`}
                      />
                    )}
                  </span>
                  <span className="flex justify-center">
                    {s.manage && (
                      <PermCheck
                        checked={perms.has(s.manage)}
                        onChange={(v) => toggle(s, "manage", v)}
                        label={`${permLabel(s.key)}: ${t("rolesPanel.colManage")}`}
                      />
                    )}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      </Modal>

      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("rolesPanel.deleteOf", { name: deleting ? adminRoleName(deleting) : "" })}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">{t("rolesPanel.deleteHint")}</p>
          <Button color="red" loading={busy} onClick={remove}>
            {t("common.delete")}
          </Button>
        </div>
      </Modal>

      {stepUpNode}
    </>
  );
}

// PermCheck is one box of the grid: the list's own compact check, not the card-style
// Checkbox, which is built for a single choice with a sentence beside it.
function PermCheck({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
}) {
  return (
    <label className="relative flex cursor-pointer items-center p-1" title={label}>
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
