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
import { fmtStamp } from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useStepUpDialog } from "./stepup";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Code,
  IconButton,
  IconCheck,
  IconClose,
  IconKey,
  IconPlus,
  IconShield,
  IconTrash,
  MICRO,
  Modal,
  Mono,
  Panel,
  PasswordInput,
  Select,
  TextInput,
  useCopy,
  useWideBox,
} from "./ui";

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

// The roster's columns, one template for the header and every row. Below WIDE_MIN it
// folds: login and role on the first line, the rest under them.
// The action track is exactly three 32px icon buttons wide. It was sized for three
// labelled buttons, and kept 224px after they became icons — which stole a hundred
// pixels from the date beside it and truncated a timestamp with room to spare.
const TPL =
  "minmax(0,1.4fr) minmax(0,1.2fr) minmax(0,.8fr) minmax(0,1.2fr) 108px";
const TPL_NARROW = "minmax(0,1fr) auto";
// The roles footnote: badge, then what the role can reach.
const LEGEND_TPL = "112px minmax(0,1fr)";
const WIDE_MIN = 720;

// The owner is the only role with a brand badge — it is the one that cannot be
// handed out from here.
function RoleBadge({ role }: { role: Role }) {
  return (
    <Badge color={role === "owner" ? "brand" : "gray"} size="xs">
      {roleLabel(role)}
    </Badge>
  );
}

function AdminRow({
  a,
  isMe,
  wide,
  onChangeRole,
  onResetPassword,
  onDelete,
}: {
  a: Admin;
  isMe: boolean;
  wide: boolean;
  onChangeRole: (a: Admin) => void;
  onResetPassword: (a: Admin) => void;
  onDelete: (a: Admin) => void;
}) {
  // The owner is untouchable, and you are not your own administrator: your login
  // and password live in the account menu, which re-asks for the current password.
  const { t } = useTranslation();
  const locked = a.role === "owner" || isMe;
  // A tick or a cross rather than a word: this column is scanned down, not read, and
  // "включена"/"выключена" are near-identical shapes at 12px. The word stays as the
  // title and for screen readers. Narrow there is no column heading, so the micro
  // label rides along — an icon alone would not say what it is about.
  const twofaWord = t(a.totp_enabled ? "totp.on" : "totp.off");
  const twofa = (
    <span className="flex min-w-0 items-center gap-1.5" title={twofaWord}>
      {!wide && <span className={MICRO}>{t("admins.col2fa")}</span>}
      <span className={a.totp_enabled ? "text-success" : "text-warning"}>
        {a.totp_enabled ? <IconCheck size={16} /> : <IconClose size={14} />}
        <span className="sr-only">{twofaWord}</span>
      </span>
    </span>
  );
  const lastLogin = a.last_login_at ? fmtStamp(a.last_login_at) : t("admins.never");
  // Icons, not words: three labelled buttons per row is a paragraph of controls in a
  // list read for its names. Each carries its title, which is also its aria-label.
  const actions = locked ? null : (
    <span className={cn("flex gap-0.5", wide && "justify-end")}>
      <IconButton title={t("admins.role")} onClick={() => onChangeRole(a)}>
        <IconShield size={16} />
      </IconButton>
      <IconButton title={t("login.password")} onClick={() => onResetPassword(a)}>
        <IconKey size={16} />
      </IconButton>
      <IconButton
        color="red"
        title={t("common.delete")}
        onClick={() => onDelete(a)}
      >
        <IconTrash size={16} />
      </IconButton>
    </span>
  );

  return (
    <div
      className="grid items-center gap-x-3 gap-y-1 border-t border-gray-100 px-3.5 py-[7px]"
      style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-xs font-medium text-ink">{a.username}</span>
        {/* Narrow, the role belongs to the name — it is the second thing read about
            an account, not a column of its own. */}
        {!wide && <RoleBadge role={a.role} />}
        {a.must_change_password && (
          <span className="shrink-0 truncate text-[11px] text-warning">
            {t("admins.awaitingPassword")}
          </span>
        )}
      </span>

      {wide ? (
        <>
          <span className="min-w-0">
            <RoleBadge role={a.role} />
          </span>
          {twofa}
          <Mono className="truncate text-[11px] text-ink-muted">{lastLogin}</Mono>
          <span className="flex justify-end">{actions}</span>
        </>
      ) : (
        <>
          <Mono className="text-right text-[11px] text-ink-muted">{lastLogin}</Mono>
          {/* The state on the left, what you can do about it on the right — the same
              reading order the wide row has, folded onto one line. */}
          <span className="col-span-2 flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
            {twofa}
            {actions}
          </span>
        </>
      )}
    </div>
  );
}

export function AdminsSettings() {
  const { t } = useTranslation();
  const [list, setList] = useState<AdminList | null>(null);
  const [loading, setLoading] = useState(true);

  // The create form, opened from the section header.
  const [adding, setAdding] = useState(false);
  const [login, setLogin] = useState("");
  const [role, setRole] = useState<Role>("operator");
  const [password, setPassword] = useState("");
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
  const [boxRef, wide] = useWideBox(WIDE_MIN);
  const { ask, stepUpNode } = useStepUpDialog();
  const { copied, copy } = useCopy();

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
    setCreated(null);
    setAdding(true);
  };

  const add = async () => {
    if (!login.trim()) return notifyError(t("admins.needLogin"));
    if (password.length < 8) {
      return notifyError(t("password.tooShort"));
    }
    const creds = await ask({
      title: t("admins.newAdmin"),
      body: t("admins.stepUpCreate", { name: login.trim() }),
      confirmLabel: t("common.create"),
    });
    if (!creds) return;
    setBusy(true);
    try {
      await createAdmin(login.trim(), password, role, creds.password);
      // Shown once, to hand over. The account is useless until the colleague
      // replaces it at first sign-in, so there is nothing to store here.
      setCreated({ username: login.trim(), password });
      setAdding(false);
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
  };

  const saveRole = async () => {
    if (!editing) return;
    const creds = await ask({
      title: t("admins.roleOf", { name: editing.username }),
      body: roleLabel(editRole),
      confirmLabel: t("common.save"),
    });
    if (!creds) return;
    setBusy(true);
    try {
      await setAdminRole(editing.id, editRole, creds.password);
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
  };

  const saveReset = async () => {
    if (!resetting) return;
    if (newPassword.length < 8) {
      return notifyError(t("password.tooShort"));
    }
    const creds = await ask({
      title: t("admins.resetOf", { name: resetting.username }),
      body: t("admins.resetHint"),
      confirmLabel: t("usersPanel.reset"),
    });
    if (!creds) return;
    setBusy(true);
    try {
      await resetAdminPassword(resetting.id, newPassword, creds.password);
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
  };

  const remove = async () => {
    if (!deleting) return;
    const creds = await ask({
      title: t("admins.deleteOf", { name: deleting.username }),
      body: t("admins.deleteHint"),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!creds) return;
    setBusy(true);
    try {
      await deleteAdmin(deleting.id, creds.password);
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
    <div className="flex flex-col gap-3.5">
      <Panel
        title={t("nav.admins")}
        aside={
          <IconButton
            variant="filled"
            color="brand"
            title={t("admins.newAdmin")}
            onClick={openAdd}
          >
            <IconPlus />
          </IconButton>
        }
      >
        {/* The new account, and then the one look anyone gets at its password. Both
            live in the section rather than in a dialog: the credentials have to be
            copied out of here, and a dialog that closes takes them with it. */}
        {created ? (
          <div className="warning-tint flex flex-col gap-2 border-t border-gray-100 px-3.5 py-3">
            <p className="text-xs leading-relaxed text-ink">
              <Trans
                i18nKey="admins.handOverHint"
                values={{ name: created.username }}
                components={{ b: <b /> }}
              />
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Code>{created.username}</Code>
              <Code>{created.password}</Code>
              <Button
                size="xs"
                variant="outline"
                color="gray"
                onClick={() => copy(`${created.username} / ${created.password}`)}
              >
                {t(copied ? "hooks.ok" : "common.copy")}
              </Button>
              <Button size="xs" variant="light" color="gray" onClick={() => setCreated(null)}>
                {t("common.done")}
              </Button>
            </div>
          </div>
        ) : null}

        <div ref={boxRef}>
          {wide && (
            <div
              className={cn(MICRO, "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2")}
              style={{ gridTemplateColumns: TPL }}
            >
              <span className="truncate">{t("admins.colLogin")}</span>
              <span className="truncate">{t("admins.colRole")}</span>
              <span className="truncate">{t("admins.col2fa")}</span>
              <span className="truncate">{t("admins.colLastLogin")}</span>
              <span />
            </div>
          )}
          {list.admins.map((a) => (
            <AdminRow
              key={a.id}
              a={a}
              isMe={a.id === list.me}
              wide={wide}
              onChangeRole={openRole}
              onResetPassword={openReset}
              onDelete={openDelete}
            />
          ))}
        </div>

        {/* What each role can reach, so handing one out is an informed choice. Rows
            rather than three columns: the badges then line up under one another and
            the sentences get the width they need at any size. */}
        <div className="border-t border-brand-600/10">
          {ALL_ROLES.map((r) => (
            <div
              key={r}
              className="grid items-baseline gap-3 border-t border-gray-100 px-3.5 py-[7px] first:border-t-0"
              style={{ gridTemplateColumns: LEGEND_TPL }}
            >
              <span>
                <RoleBadge role={r} />
              </span>
              <p className="text-[11px] leading-relaxed text-ink-muted">{roleHint(r)}</p>
            </div>
          ))}
        </div>
      </Panel>

      <AdminAuditPanel />

      {/* Create. A dialog, not a band: it asks for the owner's own password, and
          that is a question to answer in one place rather than in a page's margin.
          What comes back — the one-time password — lands in the section, where it
          can be copied without a dialog closing over it. */}
      <Modal
        open={adding}
        onClose={() => setAdding(false)}
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
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setAdding(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={add}>
              {t("common.create")}
            </Button>
          </div>
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
          <Button color="red" loading={busy} onClick={remove}>
            {t("common.delete")}
          </Button>
        </div>
      </Modal>

      {/* Every action here is re-authorised in its own dialog rather than by a
          password field parked at the bottom of each form. */}
      {stepUpNode}
    </div>
  );
}
