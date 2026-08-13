import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type ApiKey,
  type ApiKeysInfo,
  createApiKey,
  getApiKeys,
  revokeApiKey,
  setApiPath,
} from "./api";
import { useShowMore } from "./hooks";
import i18n, { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  IconChevron,
  IconCopy,
  IconShield,
  Modal,
  SaveBar,
  SettingCard,
  ShowMore,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
} from "./ui";

function fmtTs(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

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

// KeyRow is one API key in the list.
function KeyRow({ k, onRevoke }: { k: ApiKey; onRevoke: (k: ApiKey) => void }) {
  const { t } = useTranslation();
  const revoked = !!k.revoked_at;
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-3 rounded-xl border border-gray-200 px-3 py-2.5",
        revoked && "opacity-60",
      )}
    >
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-semibold text-ink">{k.name}</span>
          <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-ink-muted">
            {k.prefix}…
          </code>
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          {t("api.createdAt", { date: fmtTs(k.created_at) })} ·{" "}
          {t("api.usedAt", {
            date: k.last_used_at ? fmtTs(k.last_used_at) : t("admins.never"),
          })}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {revoked ? (
          <Badge color="gray">{t("api.revoked")}</Badge>
        ) : (
          <Badge color="green">{t("api.active")}</Badge>
        )}
        {!revoked && (
          <Button size="sm" variant="light" color="red" onClick={() => onRevoke(k)}>
            {t("api.revoke")}
          </Button>
        )}
      </div>
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
  const shownKeys = useShowMore(info?.keys ?? [], { first: 10, step: 10 });
  // Draft of the enable toggle — applied via the bottom SaveBar (not instantly),
  // matching the other settings sections. Key create/revoke/rotate stay immediate.
  const [enabledDraft, setEnabledDraft] = useState(false);
  const [saving, setSaving] = useState(false);
  const { confirm, confirmNode } = useConfirm();

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
    <div className="flex flex-col gap-4">
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
          <div className="mt-4 flex flex-col gap-2">
            {shownKeys.shown.map((k) => (
              <KeyRow key={k.id} k={k} onRevoke={revoke} />
            ))}
            {/* Keys accumulate — a revoked one is kept as a record — so an install
                that has been running for a while lists more of them than anybody
                reads at once. */}
            <ShowMore rest={shownKeys.rest} onClick={shownKeys.showMore} />
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
