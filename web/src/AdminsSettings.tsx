import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { AdminAuditPanel } from "./AdminAuditPanel";
import {
  type Admin,
  type AdminList,
  createAdmin,
  deleteAdmin,
  listAdmins,
  resetAdminPassword,
  type Role,
  roleHint,
  roleLabel,
  setAdminRole,
} from "./api";
import { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Modal,
  PasswordInput,
  Select,
  SettingCard,
  TextInput,
} from "./ui";

function fmtTs(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

// The roles an owner can hand out. The owner role is absent on purpose: ownership
// is singular, and the server refuses to grant it (see model.GrantableRoles).
const roleOptions = (): { value: Role; label: string }[] => [
  { value: "admin", label: roleLabel("admin") },
  { value: "operator", label: roleLabel("operator") },
];

// ALL_ROLES drives the legend below the roster, owner included.
const ALL_ROLES: Role[] = ["owner", "admin", "operator"];

// A password the owner will read out or paste into a chat — memorable enough to
// survive the trip, and replaced by the colleague at first sign-in anyway.
function suggestPassword(): string {
  const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = new Uint32Array(14);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

function RoleBadge({ role }: { role: Role }) {
  if (role === "owner") return <Badge color="brand">{roleLabel("owner")}</Badge>;
  if (role === "admin") return <Badge color="green">{roleLabel("admin")}</Badge>;
  return <Badge color="orange">{roleLabel("operator")}</Badge>;
}

function AdminRow({
  a,
  isMe,
  onChangeRole,
  onResetPassword,
  onDelete,
}: {
  a: Admin;
  isMe: boolean;
  onChangeRole: (a: Admin) => void;
  onResetPassword: (a: Admin) => void;
  onDelete: (a: Admin) => void;
}) {
  // The owner is untouchable, and you are not your own administrator: your login
  // and password live in the account menu, which re-asks for the current password.
  const { t } = useTranslation();
  const locked = a.role === "owner" || isMe;
  return (
    // Narrow screens stack the identity above the actions; from sm up they sit on
    // one row. The action group must be allowed to wrap (three buttons plus a badge
    // don't fit a phone), and the identity column needs min-w-0 or its long
    // created/last-login line would rather squeeze the column to nothing than wrap.
    <div className="flex flex-col gap-2 rounded-xl border border-gray-200 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="truncate font-semibold text-ink">{a.username}</span>
          <RoleBadge role={a.role} />
          {isMe && <span className="text-xs text-ink-muted">{t("admins.itsYou")}</span>}
          {a.must_change_password && (
            <Badge color="orange">{t("admins.awaitingPassword")}</Badge>
          )}
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          {t("admins.createdAt", { date: fmtTs(a.created_at) })} ·{" "}
          {t("admins.lastLogin", {
            date: a.last_login_at ? fmtTs(a.last_login_at) : t("admins.never"),
          })}
        </div>
      </div>
      {/* The owner's row and your own have no actions at all, so the whole group is
          left out rather than rendered empty — an empty flex child would still eat a
          gap under the identity block on a phone. */}
      {!locked && (
        <div className="flex flex-wrap items-center gap-2 sm:shrink-0 sm:justify-end">
          <Button
            size="sm"
            variant="light"
            color="gray"
            onClick={() => onChangeRole(a)}
          >
            {t("admins.role")}
          </Button>
          <Button
            size="sm"
            variant="light"
            color="gray"
            onClick={() => onResetPassword(a)}
          >
            {t("login.password")}
          </Button>
          <Button
            size="sm"
            variant="light"
            color="red"
            onClick={() => onDelete(a)}
          >
            {t("common.delete")}
          </Button>
        </div>
      )}
    </div>
  );
}

export function AdminsSettings() {
  const { t } = useTranslation();
  const [list, setList] = useState<AdminList | null>(null);
  const [loading, setLoading] = useState(true);

  // Create dialog.
  const [addOpen, setAddOpen] = useState(false);
  const [login, setLogin] = useState("");
  const [role, setRole] = useState<Role>("operator");
  const [password, setPassword] = useState("");
  const [current, setCurrent] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ username: string; password: string } | null>(
    null,
  );

  // Role / password dialogs for an existing admin.
  const [editing, setEditing] = useState<Admin | null>(null);
  const [editRole, setEditRole] = useState<Role>("operator");
  const [resetting, setResetting] = useState<Admin | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [deleting, setDeleting] = useState<Admin | null>(null);

  const refresh = () =>
    listAdmins()
      .then(setList)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  useEffect(() => {
    refresh();
  }, []);

  const openAdd = () => {
    setLogin("");
    setRole("operator");
    setPassword(suggestPassword());
    setCurrent("");
    setAddOpen(true);
  };

  const add = async () => {
    if (!login.trim()) return notifyError(t("admins.needLogin"));
    if (password.length < 8) {
      return notifyError(t("password.tooShort"));
    }
    if (!current) return notifyError(t("admins.needOwnPassword"));
    setBusy(true);
    try {
      await createAdmin(login.trim(), password, role, current);
      // Shown once, to hand over. The account is useless until the colleague
      // replaces it at first sign-in, so there is nothing to store here.
      setCreated({ username: login.trim(), password });
      setAddOpen(false);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const openRole = (a: Admin) => {
    setEditing(a);
    setEditRole(a.role);
    setCurrent("");
  };

  const saveRole = async () => {
    if (!editing) return;
    if (!current) return notifyError(t("admins.needOwnPassword"));
    setBusy(true);
    try {
      await setAdminRole(editing.id, editRole, current);
      notifySuccess(`${editing.username}: ${roleLabel(editRole).toLowerCase()}`);
      setEditing(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const openReset = (a: Admin) => {
    setResetting(a);
    setNewPassword(suggestPassword());
    setCurrent("");
  };

  const saveReset = async () => {
    if (!resetting) return;
    if (newPassword.length < 8) {
      return notifyError(t("password.tooShort"));
    }
    if (!current) return notifyError(t("admins.needOwnPassword"));
    setBusy(true);
    try {
      await resetAdminPassword(resetting.id, newPassword, current);
      setCreated({ username: resetting.username, password: newPassword });
      setResetting(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const openDelete = (a: Admin) => {
    setDeleting(a);
    setCurrent("");
  };

  const remove = async () => {
    if (!deleting) return;
    if (!current) return notifyError(t("admins.needOwnPassword"));
    setBusy(true);
    try {
      await deleteAdmin(deleting.id, current);
      notifySuccess(t("admins.deleted", { name: deleting.username }));
      setDeleting(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  if (loading) return <CenterLoader />;
  if (!list) return null;

  return (
    <div className="flex flex-col gap-4">
      <SettingCard
        title={t("nav.admins")}
        description={t("admins.description")}
        action={<Button onClick={openAdd}>{t("common.add")}</Button>}
        stackAction
      >
        <div className="flex flex-col gap-2">
          {list.admins.map((a) => (
            <AdminRow
              key={a.id}
              a={a}
              isMe={a.id === list.me}
              onChangeRole={openRole}
              onResetPassword={openReset}
              onDelete={openDelete}
            />
          ))}
        </div>

        <div className="mt-4 grid gap-2 sm:grid-cols-3">
          {ALL_ROLES.map((r) => (
            <div
              key={r}
              className={cn(
                "rounded-xl border border-gray-200 bg-gray-50 p-3",
                r === "owner" && "opacity-70",
              )}
            >
              <div className="mb-1">
                <RoleBadge role={r} />
              </div>
              <p className="text-xs text-ink-muted">{roleHint(r)}</p>
            </div>
          ))}
        </div>
      </SettingCard>

      <AdminAuditPanel />

      {/* Create */}
      <Modal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title={t("admins.newAdmin")}
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("login.username")}
            value={login}
            onChange={setLogin}
            placeholder={t("admins.loginPlaceholder")}
            autoFocus
          />
          <Select
            label={t("admins.role")}
            value={role}
            onChange={(v) => setRole(v as Role)}
            data={roleOptions()}
          />
          <PasswordInput
            label={t("admins.tempPassword")}
            placeholder={t("admins.tempPasswordHint")}
            value={password}
            onChange={setPassword}
          />
          <PasswordInput
            label={t("admins.yourPassword")}
            placeholder={t("creds.toConfirm")}
            value={current}
            onChange={setCurrent}
          />
          <Button loading={busy} onClick={add}>
            {t("common.create")}
          </Button>
        </div>
      </Modal>

      {/* Change role */}
      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={t("admins.roleOf", { name: editing?.username ?? "" })}
      >
        <div className="flex flex-col gap-3">
          <Select
            label={t("admins.role")}
            value={editRole}
            onChange={(v) => setEditRole(v as Role)}
            data={roleOptions()}
          />
          <PasswordInput
            label={t("admins.yourPassword")}
            placeholder={t("creds.toConfirm")}
            value={current}
            onChange={setCurrent}
          />
          <Button loading={busy} onClick={saveRole}>
            {t("common.save")}
          </Button>
        </div>
      </Modal>

      {/* Reset password */}
      <Modal
        open={!!resetting}
        onClose={() => setResetting(null)}
        title={t("admins.resetOf", { name: resetting?.username ?? "" })}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            {t("admins.resetHint")}
          </p>
          <PasswordInput
            label={t("admins.newTempPassword")}
            value={newPassword}
            onChange={setNewPassword}
          />
          <PasswordInput
            label={t("admins.yourPassword")}
            placeholder={t("creds.toConfirm")}
            value={current}
            onChange={setCurrent}
          />
          <Button loading={busy} onClick={saveReset}>
            {t("usersPanel.reset")}
          </Button>
        </div>
      </Modal>

      {/* Delete */}
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("admins.deleteOf", { name: deleting?.username ?? "" })}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            {t("admins.deleteHint")}
          </p>
          <PasswordInput
            label={t("admins.yourPassword")}
            placeholder={t("creds.toConfirm")}
            value={current}
            onChange={setCurrent}
            autoFocus
          />
          <Button color="red" loading={busy} onClick={remove}>
            {t("common.delete")}
          </Button>
        </div>
      </Modal>

      {/* One-time reveal of the password to hand over. */}
      <Modal
        open={!!created}
        onClose={() => setCreated(null)}
        title={t("admins.handOver")}
      >
        <p className="text-sm text-ink-muted">
          <Trans
            i18nKey="admins.handOverHint"
            values={{ name: created?.username ?? "" }}
            components={{ b: <b /> }}
          />
        </p>
        <div className="mt-3 flex flex-col gap-2">
          <div className="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
            <div className="text-xs text-ink-muted">{t("login.username")}</div>
            <code className="font-mono text-sm text-ink">{created?.username}</code>
          </div>
          <div className="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
            <div className="text-xs text-ink-muted">{t("login.password")}</div>
            <code className="font-mono text-sm text-ink">{created?.password}</code>
          </div>
        </div>
        <div className="mt-5 flex justify-end">
          <Button onClick={() => setCreated(null)}>{t("common.done")}</Button>
        </div>
      </Modal>
    </div>
  );
}
