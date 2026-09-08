import { type ReactNode, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type ApiKey,
  type ApiKeysInfo,
  createApiKey,
  getApiKeys,
  revokeApiKey,
  setApiPath,
} from "./api";
import { fmtStamp } from "./format";
import { useShowMore } from "./hooks";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  cn,
  IconButton,
  IconClose,
  IconChevron,
  IconCopy,
  IconShield,
  MICRO,
  Modal,
  Mono,
  SaveBar,
  SettingCard,
  ShowMore,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
  useWideBox,
} from "./ui";

/* small inline glyphs for the docs tiles (match the stroke style of ui.tsx) */
const IconDoc = ({ size = 18 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={2}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M4 4a2 2 0 0 1 2-2h8l6 6v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z" />
    <path d="M14 2v6h6" />
    <path d="M8 13h8M8 17h5" />
  </svg>
);
const IconBraces = ({ size = 18 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth={2}
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M8 3H7a2 2 0 0 0-2 2v4a2 2 0 0 1-2 2 2 2 0 0 1 2 2v4a2 2 0 0 0 2 2h1" />
    <path d="M16 3h1a2 2 0 0 1 2 2v4a2 2 0 0 1 2 2 2 2 0 0 1-2 2v4a2 2 0 0 1-2 2h-1" />
  </svg>
);

// CopyField is a read-only monospace value with a copy button.
function CopyField({ value }: { value: string }) {
  const { copied, copy } = useCopy();
  return (
    <div className="flex items-stretch gap-2">
      <code className="min-w-0 flex-1 truncate rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-ink">
        {value}
      </code>
      <Button variant="light" color="gray" onClick={() => copy(value)}>
        <IconCopy /> {i18n.t(copied ? "common.copied" : "common.copy")}
      </Button>
    </div>
  );
}

// DocTile is one clickable documentation destination.
function DocTile({
  href,
  icon,
  title,
  subtitle,
}: {
  href: string;
  icon: ReactNode;
  title: string;
  subtitle: string;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="group flex items-center gap-3 rounded-xl border border-gray-200 bg-white p-3 transition hover:border-accent hover:accent-tint"
    >
      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg accent-tint text-accent">
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-semibold text-ink">{title}</span>
        <span className="block truncate text-xs text-ink-muted">{subtitle}</span>
      </span>
      <IconChevron
        className="-rotate-90 text-ink-muted transition group-hover:text-accent"
        size={18}
      />
    </a>
  );
}

// The key roster's columns, the same shape every other list in the panel has.
const TPL =
  "minmax(0,1.4fr) minmax(0,1fr) minmax(0,1fr) minmax(0,.7fr) 40px";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 560;

// KeyRow is one API key: what it is called, what it starts with, when it was minted
// and last used, and whether it still works.
function KeyRow({
  k,
  wide,
  onRevoke,
}: {
  k: ApiKey;
  wide: boolean;
  onRevoke: (k: ApiKey) => void;
}) {
  const { t } = useTranslation();
  const revoked = !!k.revoked_at;
  // Never called reads as a dash, like every other empty value in a column —
  // "ни разу" is a sentence where the column already asks the question.
  const used = fmtStamp(k.last_used_at);
  const status = (
    <span className={cn("truncate text-xs", revoked ? "text-ink-muted" : "text-success")}>
      {t(revoked ? "api.revoked" : "api.active")}
    </span>
  );
  // An icon, like the other row actions in the panel; the word lives in its title.
  const action = revoked ? null : (
    <IconButton color="red" title={t("api.revoke")} onClick={() => onRevoke(k)}>
      <IconClose size={16} />
    </IconButton>
  );
  return (
    <div
      className={cn(
        "grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]",
        revoked && "opacity-60",
      )}
      style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-xs font-medium text-ink">{k.name}</span>
        <Mono className="shrink-0 text-[11px] text-ink-muted">{k.prefix}…</Mono>
      </span>
      {wide ? (
        <>
          <Mono className="truncate text-[11px] text-ink-muted">
            {fmtStamp(k.created_at)}
          </Mono>
          <Mono className="truncate text-[11px] text-ink-muted">{used}</Mono>
          {status}
          <span className="flex justify-end">{action}</span>
        </>
      ) : (
        <>
          <span className="flex items-center justify-end gap-2">
            {status}
            {action}
          </span>
          <span className="col-span-2 truncate text-[11px] text-ink-muted">
            {t("api.createdAt", { date: fmtStamp(k.created_at) })} ·{" "}
            {t("api.usedAt", { date: used })}
          </span>
        </>
      )}
    </div>
  );
}

export function ApiSettings() {
  const { t } = useTranslation();
  const [info, setInfo] = useState<ApiKeysInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<ApiKey | null>(null);
  // Ten at a time: the roster grows with every key ever minted (revoked ones stay
  // as a record), and the card is a list to scan, not to scroll.
  // Active keys first: a revoked one is kept as a record, and on an install that has
  // been running a while the records outnumber the keys that still work.
  const sortedKeys = useMemo(
    () =>
      [...(info?.keys ?? [])].sort(
        (a, b) =>
          Number(!!a.revoked_at) - Number(!!b.revoked_at) ||
          b.created_at - a.created_at,
      ),
    [info],
  );
  const shownKeys = useShowMore(sortedKeys, { first: 10, step: 10 });
  // Draft of the enable toggle — applied via the bottom SaveBar (not instantly),
  // matching the other settings sections. Key create/revoke/rotate stay immediate.
  const [enabledDraft, setEnabledDraft] = useState(false);
  const [saving, setSaving] = useState(false);
  const { confirm, confirmNode } = useConfirm();
  const [keysRef, wideKeys] = useWideBox(WIDE_MIN);

  const refresh = () =>
    getApiKeys()
      .then(setInfo)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  useEffect(() => {
    refresh();
  }, []);

  // Sync the toggle draft whenever the server's enabled state changes (initial
  // load, after Save, or after rotate) — but not on a purely local flip.
  useEffect(() => {
    if (info) setEnabledDraft(info.enabled);
  }, [info?.enabled]);

  const create = async () => {
    const n = name.trim();
    if (!n) return;
    setCreating(true);
    try {
      const res = await createApiKey(n);
      setCreated(res.key);
      setName("");
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setCreating(false);
    }
  };

  const revoke = async (k: ApiKey) => {
    const ok = await confirm({
      title: t("api.revokeTitle"),
      body: t("api.revokeBody", { name: k.name }),
      confirmLabel: t("api.revoke"),
      danger: true,
    });
    if (!ok) return;
    try {
      await revokeApiKey(k.id);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const rotatePath = async () => {
    const ok = await confirm({
      title: t("api.rotateTitle"),
      body: t("api.rotateBody"),
      confirmLabel: t("api.rotateConfirm"),
      danger: true,
    });
    if (!ok) return;
    try {
      const res = await setApiPath(true, true);
      setInfo((i) => (i ? { ...i, ...res } : i));
      notifySuccess(t("api.rotated"));
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // saveEnabled applies the staged on/off toggle. Enabling mints the base URL;
  // disabling closes access but keeps the keys, which resume once turned back on.
  const saveEnabled = async () => {
    if (!info) return;
    setSaving(true);
    try {
      const res = await setApiPath(enabledDraft);
      setInfo((i) => (i ? { ...i, ...res } : i));
      if (enabledDraft) await refresh();
      notifySuccess(t(enabledDraft ? "api.enabled" : "api.disabled"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <CenterLoader />;
  if (!info) return null;

  const enabledDirty = enabledDraft !== info.enabled;

  return (
    <div className="flex flex-col gap-3.5">
      <SettingCard
        title={t("api.title")}
        description={t("api.description")}
        action={<Switch checked={enabledDraft} onChange={setEnabledDraft} />}
      >
        {info.enabled ? (
          <div className="flex flex-col gap-3">
            <div>
              <div className="mb-1 text-sm font-semibold text-ink">
                {t("api.baseUrl")}
              </div>
              <CopyField value={info.base_url} />
            </div>
            <div className="flex flex-wrap gap-2 pt-2">
              <Button size="sm" variant="light" color="gray" onClick={rotatePath}>
                {t("api.rotateConfirm")}
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-3 rounded-xl border border-dashed border-gray-200 bg-gray-50 p-4">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full accent-tint text-accent">
              <IconShield size={20} />
            </span>
            <p className="text-sm text-ink-muted">
              {t("api.offHint")}
            </p>
          </div>
        )}
      </SettingCard>

      {info.enabled && (
        <SettingCard
          title={t("api.docs")}
          description={t("api.docsHint")}
        >
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <DocTile
              href={`${info.base_url}/v1/docs`}
              icon={<IconDoc />}
              title="Swagger UI"
              subtitle={t("api.swaggerHint")}
            />
            <DocTile
              href={`${info.base_url}/v1/openapi.json`}
              icon={<IconBraces />}
              title="openapi.json"
              subtitle={t("api.openapiHint")}
            />
          </div>
          {/* The scrape target is a URL an operator pastes into a Prometheus config
              rather than opens, so it's a copy field and not a tile. */}
          <div className="pt-3">
            <p className="mb-1 text-sm font-medium text-ink">{t("api.metrics")}</p>
            <CopyField value={`${info.base_url}/v1/metrics`} />
            <p className="mt-1 text-xs text-ink-muted">{t("api.metricsHint")}</p>
          </div>
        </SettingCard>
      )}

      <SettingCard
        title={t("api.keys")}
        description={t("api.keysHint")}
      >
        <div className="flex items-end gap-2">
          <div className="flex-1">
            <TextInput
              label={t("api.newKeyName")}
              value={name}
              onChange={setName}
              placeholder={t("api.newKeyPlaceholder")}
            />
          </div>
          <Button onClick={create} loading={creating} disabled={!name.trim()}>
            {t("common.create")}
          </Button>
        </div>

        {info.keys.length > 0 ? (
          <div ref={keysRef} className="-mx-3.5 mt-3">
            {wideKeys && (
              <div
                className={cn(MICRO, "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2")}
                style={{ gridTemplateColumns: TPL }}
              >
                <span className="truncate">{t("api.colName")}</span>
                <span className="truncate">{t("api.colCreated")}</span>
                <span className="truncate">{t("api.colUsed")}</span>
                <span className="truncate">{t("api.colStatus")}</span>
                <span />
              </div>
            )}
            {shownKeys.shown.map((k) => (
              <KeyRow key={k.id} k={k} wide={wideKeys} onRevoke={revoke} />
            ))}
            {/* Keys accumulate — a revoked one is kept as a record — so an install
                that has been running for a while lists more of them than anybody
                reads at once. */}
            <ShowMore rest={shownKeys.rest} onClick={shownKeys.showMore} className="p-3.5" />
          </div>
        ) : (
          <p className="mt-4 text-center text-sm text-ink-muted">
            {t("api.noKeys")}
          </p>
        )}
      </SettingCard>

      {/* One-time reveal of a freshly created key. */}
      <Modal open={!!created} onClose={() => setCreated(null)} title={t("api.keyCreated")}>
        <p className="text-sm text-ink-muted">
          {t("api.keyCreatedHint")}
        </p>
        {created?.raw_key && (
          <div className="mt-3">
            <CopyField value={created.raw_key} />
          </div>
        )}
        <div className="mt-5 flex justify-end">
          <Button onClick={() => setCreated(null)}>{t("common.done")}</Button>
        </div>
      </Modal>

      <SaveBar
        dirty={enabledDirty}
        busy={saving}
        onSave={saveEnabled}
        onCancel={() => setEnabledDraft(info.enabled)}
      />

      {confirmNode}
    </div>
  );
}
