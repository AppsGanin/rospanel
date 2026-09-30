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
import { errMessage, notifyError, notifySuccess } from "./notify";
import { PermGrid } from "./PermGrid";
import { useStepUpDialog } from "./stepup";
import {
  Button,
  IconButton,
  IconPencil,
  IconPlus,
  IconTrash,
  Modal,
  Panel,
  TextInput,
} from "./ui";

// The roles: what each admin may see and do. The owner's alone, like the roster
// above it. The permission rows come from the server's catalog (GET /api/roles), so
// a permission added there reaches this editor without a second list to keep in step.

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
          const held = r.admins > 0;
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
                  {t("rolesPanel.holders", { admins: r.admins })}
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
          <PermGrid catalog={catalog} implies={implies} perms={perms} onChange={setPerms} />
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
