import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  configurePlugin,
  disablePlugin,
  enablePlugin,
  getPluginCode,
  getPluginLogs,
  inspectPluginFile,
  inspectPluginURL,
  installPlugin,
  listPlugins,
  type Perm,
  type PluginField,
  type PluginInfo,
  type PluginInspection,
  type PluginLogLine,
  type PluginManifest,
  type PluginText,
  rollbackPlugin,
  uninstallPlugin,
  updatePlugin,
} from "./api";
import { fmtBytes, fmtStamp } from "./format";
import i18n, { td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { forgetPluginActions, PluginActionButtons } from "./PluginSurfaces";
import { useCan } from "./role";
import {
  Badge,
  Button,
  CenterLoader,
  Checkbox,
  Code,
  cn,
  EmptyState,
  Modal,
  Panel,
  PasswordInput,
  Select,
  Switch,
  Textarea,
  TextInput,
} from "./ui";

// Plugins: what the operator installed, what each may do, and its switches. The
// consent screen is the important part — it is where the operator learns what a
// package asks for before it runs, and what they agree to is sent back to the
// server and checked against the package byte for byte (see panel_plugins.go).

// pickText reads a plugin's own string: plain, or per language.
export function pickText(t: PluginText | undefined, lang = i18n.language): string {
  if (!t) return "";
  if (typeof t === "string") return t;
  for (const k of [lang, "en", "ru", ""]) if (t[k]) return t[k];
  return Object.values(t)[0] ?? "";
}

// Permissions are labelled with the role editor's sections (permSection.*): one
// vocabulary for "what this admin may do" and "what this plugin may do".
const PERM_SECTION: Partial<Record<Perm, [string, "read" | "write" | null]>> = {
  "users.delete": ["usersDelete", null],
  "users.export": ["usersExport", null],
  "payments.manage": ["payments", null],
  "broadcasts.manage": ["broadcasts", null],
  "logs.view": ["logs", null],
  "audit.view": ["audit", null],
  "billing.sell": ["billing", "write"],
};

export function permLabel(p: Perm): string {
  const [section, act] =
    PERM_SECTION[p] ?? [p.split(".")[0], p.endsWith(".view") ? "read" : "write"];
  const name = td(`permSection.${section}`);
  return act ? `${name} — ${i18n.t(`plugins.${act}`)}` : name;
}

// points lists in words what the manifest says the plugin plugs into.
function points(m: PluginManifest): string[] {
  const p = m.provides ?? {};
  const out: string[] = [];
  if (p.events?.length) out.push(i18n.t("plugins.point.events", { list: p.events.join(", ") }));
  if (p.cron?.length) out.push(i18n.t("plugins.point.cron"));
  if (p.payment) out.push(i18n.t("plugins.point.payment"));
  if (p.http) out.push(i18n.t("plugins.point.http"));
  if (p.hooks?.length) out.push(i18n.t("plugins.point.hooks"));
  if (p.channel) out.push(i18n.t("plugins.point.channel"));
  if (p.user_fields?.length) out.push(i18n.t("plugins.point.user_fields"));
  if (p.actions?.length) out.push(i18n.t("plugins.point.actions"));
  if (p.widgets?.length) out.push(i18n.t("plugins.point.widgets"));
  if (p.sub_blocks) out.push(i18n.t("plugins.point.sub_blocks"));
  if (p.bot) out.push(i18n.t("plugins.point.bot"));
  if (p.price) out.push(i18n.t("plugins.point.price"));
  if (p.subscription) out.push(i18n.t("plugins.point.subscription"));
  if (p.theme) out.push(i18n.t("plugins.point.theme"));
  return out;
}

const STATUS: Record<PluginInfo["status"], { key: string; color: "green" | "gray" | "orange" | "red" }> = {
  active: { key: "plugins.statusActive", color: "green" },
  disabled: { key: "plugins.statusDisabled", color: "gray" },
  paused: { key: "plugins.statusPaused", color: "orange" },
  error: { key: "plugins.statusError", color: "red" },
};

export function PluginsPanel() {
  const { t } = useTranslation();
  const canManage = useCan("plugins.manage");
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [installing, setInstalling] = useState<{ update?: PluginInfo } | null>(null);
  const [logsOf, setLogsOf] = useState<PluginInfo | null>(null);
  const [codeOf, setCodeOf] = useState<PluginInfo | null>(null);
  const [removing, setRemoving] = useState<PluginInfo | null>(null);

  const load = () =>
    listPlugins()
      .then((r) => setPlugins(r.plugins))
      .catch((e) => notifyError(errMessage(e)));
  // biome-ignore lint/correctness/useExhaustiveDependencies: once, on mount
  useEffect(() => {
    load();
  }, []);

  const replace = (p: PluginInfo) =>
    setPlugins((cur) => (cur ?? []).map((x) => (x.id === p.id ? p : x)));

  return (
    <>
      <Panel
        title={
          <span className="flex items-center gap-2">
            {t("plugins.title")}
            <Badge color="orange" size="xs">
              {t("plugins.beta")}
            </Badge>
          </span>
        }
        aside={
          canManage && (
            <Button size="sm" onClick={() => setInstalling({})}>
              {t("plugins.install")}
            </Button>
          )
        }
      >
        <p className="border-b border-brand-600/10 px-3.5 py-3 text-xs text-ink-muted">{t("plugins.intro")}</p>
        {plugins === null ? (
          <CenterLoader />
        ) : plugins.length === 0 ? (
          <EmptyState title={t("plugins.empty")} body={t("plugins.emptyHint")} />
        ) : (
          <div className="divide-y divide-brand-600/10">
            {plugins.map((p) => (
              <PluginRow
                key={p.id}
                plugin={p}
                canManage={canManage}
                onChange={replace}
                onLogs={() => setLogsOf(p)}
                onCode={() => setCodeOf(p)}
                onUpdate={() => setInstalling({ update: p })}
                onRemove={() => setRemoving(p)}
              />
            ))}
          </div>
        )}
      </Panel>


      {installing && (
        <InstallDialog
          update={installing.update}
          onClose={() => setInstalling(null)}
          onDone={() => {
            setInstalling(null);
            load();
          }}
        />
      )}
      {logsOf && <LogsDialog plugin={logsOf} onClose={() => setLogsOf(null)} />}
      {codeOf && <CodeDialog plugin={codeOf} onClose={() => setCodeOf(null)} />}
      {removing && (
        <RemoveDialog
          plugin={removing}
          onClose={() => setRemoving(null)}
          onDone={() => {
            setRemoving(null);
            load();
          }}
        />
      )}
    </>
  );
}

function PluginRow({
  plugin: p,
  canManage,
  onChange,
  onLogs,
  onCode,
  onUpdate,
  onRemove,
}: {
  plugin: PluginInfo;
  canManage: boolean;
  onChange: (p: PluginInfo) => void;
  onLogs: () => void;
  onCode: () => void;
  onUpdate: () => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const m = p.manifest;
  const status = STATUS[p.status] ?? STATUS.disabled;
  const hasSettings = (m?.settings?.length ?? 0) > 0;

  const run = async (fn: () => Promise<PluginInfo>, ok?: (p: PluginInfo) => string) => {
    setBusy(true);
    try {
      const next = await fn();
      onChange(next);
      if (ok) notifySuccess(ok(next));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = (on: boolean) => {
    forgetPluginActions();
    return run(() => (on ? enablePlugin(p.id) : disablePlugin(p.id)));
  };

  return (
    <div className="flex flex-col gap-2 px-3.5 py-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="text-sm font-semibold text-ink">{pickText(m?.name) || p.id}</span>
            <span className="text-xs text-ink-muted">{t("plugins.version", { v: p.version })}</span>
            <Badge color={status.color} size="xs">
              {td(status.key)}
            </Badge>
          </div>
          {m?.description && <p className="mt-0.5 text-xs text-ink-muted">{pickText(m.description)}</p>}
          {p.status_error && (
            <p className="mt-1 whitespace-pre-wrap break-words text-xs text-danger">{p.status_error}</p>
          )}
          {p.missing_setup && p.missing_setup.length > 0 && p.status !== "active" && (
            <p className="mt-1 text-xs text-warning">
              {t("plugins.setupFirst", { list: settingNames(m, p.missing_setup) })}
            </p>
          )}
          <p className="mt-1 text-[11px] text-ink-muted">
            {[m?.author && t("plugins.by", { author: m.author }), t("plugins.db", { size: fmtBytes(p.db_bytes) })]
              .filter(Boolean)
              .join(" · ")}
          </p>
        </div>
        <Switch
          checked={p.status === "active"}
          disabled={!canManage || busy}
          onChange={toggle}
        />
      </div>

      {p.status === "active" && p.http_url && (
        <div>
          <p className="mb-1 text-[11px] text-ink-muted">{t("plugins.httpUrl")}</p>
          <Code block copy>
            {p.http_url}
          </Code>
        </div>
      )}
      {p.status === "active" && p.payment_key && (
        <p className="text-[11px] text-ink-muted">{t("plugins.paymentHint")}</p>
      )}

      <div className="flex flex-wrap gap-1.5">
        {hasSettings && (
          <Button size="xs" variant="light" color="gray" onClick={() => setOpen((v) => !v)}>
            {t("plugins.settings")}
          </Button>
        )}
        {p.status === "active" && <PluginActionButtons scope="global" plugin={p.id} />}
        <Button size="xs" variant="light" color="gray" onClick={onLogs}>
          {t("plugins.logs")}
        </Button>
        <Button size="xs" variant="light" color="gray" onClick={onCode}>
          {t("plugins.code")}
        </Button>
        {canManage && (
          <>
            <Button size="xs" variant="light" color="gray" onClick={onUpdate}>
              {t("plugins.update")}
            </Button>
            {p.prev_version && (
              <Button
                size="xs"
                variant="light"
                color="gray"
                loading={busy}
                onClick={() =>
                  run(
                    () => rollbackPlugin(p.id),
                    (n) => t("plugins.rolledBack", { v: n.version }),
                  )
                }
              >
                {t("plugins.rollback", { v: p.prev_version })}
              </Button>
            )}
            <Button size="xs" variant="light" color="red" onClick={onRemove}>
              {t("plugins.remove")}
            </Button>
          </>
        )}
      </div>

      {open && m && (
        <SettingsForm
          plugin={p}
          fields={m.settings ?? []}
          canManage={canManage}
          onSaved={(n) => {
            onChange(n);
            notifySuccess(t("plugins.saved"));
          }}
        />
      )}
    </div>
  );
}

function settingNames(m: PluginManifest | null, keys: string[]): string {
  return keys.map((k) => pickText(m?.settings?.find((f) => f.key === k)?.label) || k).join(", ");
}

// SettingsForm is a plugin's settings, drawn from its manifest. Secrets are
// write-only: blank keeps what is stored.
function SettingsForm({
  plugin,
  fields,
  canManage,
  onSaved,
}: {
  plugin: PluginInfo;
  fields: PluginField[];
  canManage: boolean;
  onSaved: (p: PluginInfo) => void;
}) {
  const { t } = useTranslation();
  const initial = () => {
    const v: Record<string, string> = {};
    for (const f of fields) v[f.key] = f.kind === "secret" ? "" : (plugin.config[f.key] ?? "");
    return v;
  };
  const [values, setValues] = useState(initial);
  const [saving, setSaving] = useState(false);
  const set = (k: string, v: string) => setValues((cur) => ({ ...cur, [k]: v }));

  const save = async () => {
    setSaving(true);
    try {
      const next = await configurePlugin(plugin.id, values);
      onSaved(next);
      setValues((cur) => {
        const out = { ...cur };
        for (const f of fields) if (f.kind === "secret") out[f.key] = "";
        return out;
      });
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-2.5 rounded-lg border border-brand-600/10 p-3">
      <div className="grid gap-2.5 sm:grid-cols-2">
        {fields.map((f) => {
          const label = pickText(f.label);
          const help = pickText(f.help);
          const hint = help && <p className="mt-1 text-[11px] text-ink-muted">{help}</p>;
          if (f.kind === "bool") {
            return (
              <label key={f.key} className="flex items-center gap-2 text-xs text-ink">
                <Switch
                  checked={values[f.key] === "1" || values[f.key] === "true"}
                  onChange={(v) => set(f.key, v ? "1" : "")}
                  disabled={!canManage}
                />
                {label}
                {help && <span className="text-[11px] text-ink-muted">— {help}</span>}
              </label>
            );
          }
          if (f.kind === "select") {
            const opts = (f.options ?? []).map((o) => ({ value: o.value, label: pickText(o.label) }));
            return (
              <div key={f.key}>
                <Select
                  label={label}
                  data={opts}
                  value={values[f.key] || opts[0]?.value || ""}
                  onChange={(v) => set(f.key, v)}
                  disabled={!canManage}
                />
                {hint}
              </div>
            );
          }
          if (f.kind === "textarea") {
            return (
              <div key={f.key} className="sm:col-span-2">
                <Textarea label={label} value={values[f.key]} onChange={(v) => set(f.key, v)} hint={help} />
              </div>
            );
          }
          const secretSet = f.kind === "secret" && plugin.secrets_set?.includes(f.key);
          return (
            <div key={f.key}>
              <TextInput
                label={secretSet ? t("plugins.secretSet", { label }) : label}
                value={values[f.key]}
                onChange={(v) => set(f.key, v)}
                placeholder={secretSet ? "••••••••" : (f.placeholder ?? "")}
                type={f.kind === "number" ? "number" : undefined}
                disabled={!canManage}
              />
              {hint}
            </div>
          );
        })}
      </div>
      {canManage && (
        <div className="flex justify-end">
          <Button size="sm" loading={saving} onClick={save}>
            {t("common.save")}
          </Button>
        </div>
      )}
    </div>
  );
}

// InstallDialog uploads a package, shows what it asks for, and installs (or
// updates) it with the admin's password.
function InstallDialog({
  update,
  onClose,
  onDone,
}: {
  update?: PluginInfo;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const fileRef = useRef<HTMLInputElement>(null);
  const [url, setURL] = useState("");
  const [checking, setChecking] = useState(false);
  const [review, setReview] = useState<PluginInspection | null>(null);
  const [password, setPassword] = useState("");
  const [saving, setSaving] = useState(false);

  const inspect = async (fn: () => Promise<PluginInspection>) => {
    setChecking(true);
    try {
      const r = await fn();
      if (update && r.manifest.id !== update.id) {
        notifyError(t("err.pluginWrongID", { detail: r.manifest.id }));
        return;
      }
      setReview(r);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setChecking(false);
    }
  };

  const confirm = async () => {
    if (!review) return;
    setSaving(true);
    try {
      const name = pickText(review.manifest.name);
      if (update) {
        const n = await updatePlugin(update.id, review, password);
        notifySuccess(t("plugins.updatedTo", { v: n.version }));
      } else {
        await installPlugin(review, password);
        notifySuccess(t("plugins.installed", { name }));
      }
      onDone();
    } catch (e) {
      notifyError(errMessage(e));
      // A refused password leaves the upload waiting on the server; anything past it
      // (a changed package, an expired upload, a failed start) spends it — start over.
      const code = (e as { code?: string }).code;
      if (code !== "err.wrongPassword" && code !== "err.tooManyAttempts") setReview(null);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={update ? t("plugins.updateTitle") : t("plugins.installTitle")}
      footer={
        review && (
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setReview(null)}>
              {t("common.back")}
            </Button>
            <Button loading={saving} disabled={!password} onClick={confirm}>
              {update ? t("plugins.confirmUpdate") : t("plugins.confirmInstall")}
            </Button>
          </div>
        )
      }
    >
      {!review ? (
        <div className="flex flex-col gap-3">
          <div>
            <p className="mb-1 text-xs font-medium text-ink">{t("plugins.file")}</p>
            <input
              ref={fileRef}
              type="file"
              accept=".zip,application/zip"
              className="hidden"
              onChange={(e) => {
                const f = e.currentTarget.files?.[0];
                if (f) inspect(() => inspectPluginFile(f));
                e.currentTarget.value = "";
              }}
            />
            <Button variant="light" loading={checking} onClick={() => fileRef.current?.click()}>
              {t("plugins.chooseFile")}
            </Button>
          </div>
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <TextInput label={t("plugins.url")} value={url} onChange={setURL} placeholder="https://…/plugin.zip" />
            </div>
            <Button variant="light" disabled={!url.trim()} loading={checking} onClick={() => inspect(() => inspectPluginURL(url.trim()))}>
              {t("plugins.check")}
            </Button>
          </div>
        </div>
      ) : (
        <Consent review={review} password={password} onPassword={setPassword} />
      )}
    </Modal>
  );
}

// Consent is the review screen: everything the package asks for, in words.
function Consent({
  review: r,
  password,
  onPassword,
}: {
  review: PluginInspection;
  password: string;
  onPassword: (v: string) => void;
}) {
  const { t } = useTranslation();
  const m = r.manifest;
  const perms = m.permissions ?? [];
  const net = m.net ?? [];
  const does = points(m);
  return (
    <div className="flex flex-col gap-4 text-sm">
      <div>
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="text-base font-semibold text-ink">{pickText(m.name)}</span>
          <span className="text-xs text-ink-muted">{t("plugins.version", { v: m.version })}</span>
        </div>
        {m.description && <p className="mt-1 text-xs text-ink-muted">{pickText(m.description)}</p>}
        <p className="mt-1 text-[11px] text-ink-muted">
          {[m.author && t("plugins.by", { author: m.author }), m.homepage, `sha256 ${r.sha256.slice(0, 12)}…`]
            .filter(Boolean)
            .join(" · ")}
        </p>
        {r.installed && <p className="mt-1 text-xs text-ink">{t("plugins.replaces", { v: r.installed })}</p>}
      </div>

      <ConsentList title={t("plugins.asks")} empty={t("plugins.permsNone")}>
        {perms.map((p) => (
          <li key={p} className="flex flex-wrap items-center gap-1.5">
            {permLabel(p)}
            {r.risky_perms?.includes(p) && (
              <Badge color="red" size="xs">
                {t("plugins.risky")}
              </Badge>
            )}
            {r.added_perms?.includes(p) && (
              <Badge color="orange" size="xs">
                {t("plugins.newInVersion")}
              </Badge>
            )}
          </li>
        ))}
      </ConsentList>

      <ConsentList title={t("plugins.net")} empty={t("plugins.netNone")}>
        {net.map((h) => (
          <li key={h} className="flex flex-wrap items-center gap-1.5">
            <span className="font-mono text-xs">{h}</span>
            {r.added_net?.includes(h) && (
              <Badge color="orange" size="xs">
                {t("plugins.newInVersion")}
              </Badge>
            )}
          </li>
        ))}
      </ConsentList>

      {does.length > 0 && (
        <ConsentList title={t("plugins.does")} empty="">
          {does.map((d) => (
            <li key={d}>{d}</li>
          ))}
        </ConsentList>
      )}

      {m.experimental && m.experimental.length > 0 && (
        <p className="text-xs text-warning">{t("plugins.experimental", { list: m.experimental.join(", ") })}</p>
      )}
      <p className="text-xs text-ink-muted">{t("plugins.reviewCode")}</p>
      <PasswordInput label={t("creds.currentPassword")} value={password} onChange={onPassword} autoFocus />
    </div>
  );
}

function ConsentList({ title, empty, children }: { title: string; empty: string; children: React.ReactNode[] }) {
  return (
    <div>
      <p className="mb-1 text-xs font-semibold text-ink">{title}</p>
      {children.length === 0 ? (
        <p className="text-xs text-ink-muted">{empty}</p>
      ) : (
        <ul className="flex list-disc flex-col gap-1 pl-5 text-xs text-ink">{children}</ul>
      )}
    </div>
  );
}

function LogsDialog({ plugin, onClose }: { plugin: PluginInfo; onClose: () => void }) {
  const { t } = useTranslation();
  const [lines, setLines] = useState<PluginLogLine[] | null>(null);
  const load = () =>
    getPluginLogs(plugin.id)
      .then((r) => setLines(r.lines ?? []))
      .catch((e) => {
        setLines([]);
        notifyError(errMessage(e));
      });
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per opened plugin
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [plugin.id]);
  return (
    <Modal open onClose={onClose} size="xl" title={t("plugins.logsTitle", { name: pickText(plugin.manifest?.name) || plugin.id })}>
      {lines === null ? (
        <CenterLoader />
      ) : lines.length === 0 ? (
        <p className="py-6 text-center text-xs text-ink-muted">{t("plugins.logsEmpty")}</p>
      ) : (
        <div className="max-h-[60vh] overflow-auto rounded-lg bg-gray-50 p-2 font-mono text-[11px] leading-relaxed">
          {[...lines].reverse().map((l, i) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: a log has no ids, and it only grows
              key={i}
              className={cn(
                "whitespace-pre-wrap break-words",
                l.level === "error" ? "text-danger" : l.level === "warn" ? "text-warning" : "text-ink",
              )}
            >
              <span className="text-ink-muted">{fmtStamp(l.at)} </span>
              {l.msg}
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}

function CodeDialog({ plugin, onClose }: { plugin: PluginInfo; onClose: () => void }) {
  const { t } = useTranslation();
  const [code, setCode] = useState<string | null>(null);
  useEffect(() => {
    getPluginCode(plugin.id)
      .then((r) => setCode(r.code))
      .catch((e) => notifyError(errMessage(e)));
  }, [plugin.id]);
  return (
    <Modal open onClose={onClose} size="xl" title={t("plugins.codeTitle", { name: pickText(plugin.manifest?.name) || plugin.id })}>
      {code === null ? (
        <CenterLoader />
      ) : (
        <pre className="max-h-[65vh] overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-[11px] leading-relaxed text-ink">
          {code}
        </pre>
      )}
    </Modal>
  );
}

function RemoveDialog({ plugin, onClose, onDone }: { plugin: PluginInfo; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const [keep, setKeep] = useState(false);
  const [busy, setBusy] = useState(false);
  const name = pickText(plugin.manifest?.name) || plugin.id;
  const remove = async () => {
    setBusy(true);
    try {
      await uninstallPlugin(plugin.id, keep);
      notifySuccess(t("plugins.removed"));
      onDone();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("plugins.removeTitle", { name })}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button color="red" loading={busy} onClick={remove}>
            {t("plugins.remove")}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-sm text-ink-muted">{t("plugins.removeHint")}</p>
        <Checkbox checked={keep} onChange={setKeep} label={t("plugins.removeKeep")} />
        <p className="text-[11px] text-ink-muted">{t("plugins.db", { size: fmtBytes(plugin.db_bytes) })}</p>
      </div>
    </Modal>
  );
}
