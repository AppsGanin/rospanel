import { useEffect, useMemo, useState } from "react";
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
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  cn,
  Code,
  EmptyState,
  IconButton,
  IconClose,
  IconExternal,
  IconPlus,
  MICRO,
  Modal,
  Mono,
  Panel,
  SaveBar,
  SettingRow,
  ShowMore,
  Switch,
  TextInput,
  useConfirm,
  useWideBox,
} from "./ui";

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
  const [adding, setAdding] = useState(false);
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
      setAdding(false);
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
      {/* The API's own switch belongs to the whole section, so it sits in the header
          band beside its name. */}
      <Panel
        title={t("api.title")}
        aside={<Switch checked={enabledDraft} onChange={setEnabledDraft} />}
      >
        <SettingRow hint={t("api.description")} />
        {info.enabled ? (
          <SettingRow
            label={t("api.baseUrl")}
            control={
              <Button size="xs" variant="light" color="gray" onClick={rotatePath}>
                {t("api.rotateConfirm")}
              </Button>
            }
          >
            <Code block copy>
              {info.base_url}
            </Code>
          </SettingRow>
        ) : (
          <SettingRow hint={t("api.offHint")} />
        )}
      </Panel>

      {info.enabled && (
        <Panel title={t("api.docs")}>
          <SettingRow hint={t("api.docsHint")} />
          <SettingRow
            label="Swagger UI"
            hint={t("api.swaggerHint")}
            control={
              <IconButton
                href={`${info.base_url}/v1/docs`}
                target="_blank"
                title="Swagger UI"
              >
                <IconExternal />
              </IconButton>
            }
          />
          <SettingRow
            label="openapi.json"
            hint={t("api.openapiHint")}
            control={
              <IconButton
                href={`${info.base_url}/v1/openapi.json`}
                target="_blank"
                title="openapi.json"
              >
                <IconExternal />
              </IconButton>
            }
          />
          {/* A scrape target is pasted into a Prometheus config, not clicked. */}
          <SettingRow label={t("api.metrics")} hint={t("api.metricsHint")}>
            <Code block copy>{`${info.base_url}/v1/metrics`}</Code>
          </SettingRow>
        </Panel>
      )}

      <Panel
        title={t("api.keys")}
        aside={
          <IconButton
            variant="filled"
            color="brand"
            title={t("common.create")}
            onClick={() => {
              setName("");
              setAdding(true);
            }}
          >
            <IconPlus />
          </IconButton>
        }
      >
        <SettingRow hint={t("api.keysHint")} />
        {info.keys.length > 0 ? (
          <div ref={keysRef}>
            {wideKeys && (
              <div
                className={cn(
                  MICRO,
                  "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2",
                )}
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
            <ShowMore
              rest={shownKeys.rest}
              onClick={shownKeys.showMore}
              className="p-3.5"
            />
          </div>
        ) : (
          <EmptyState title={t("api.noKeys")} />
        )}
      </Panel>

      {/* Minting a key: a name, and then the one look anyone gets at the key. */}
      <Modal
        open={adding}
        onClose={() => setAdding(false)}
        title={t("api.keys")}
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("api.newKeyName")}
            value={name}
            onChange={setName}
            placeholder={t("api.newKeyPlaceholder")}
            autoFocus
          />
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              color="gray"
              size="sm"
              onClick={() => setAdding(false)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              onClick={create}
              loading={creating}
              disabled={!name.trim()}
            >
              {t("common.create")}
            </Button>
          </div>
        </div>
      </Modal>

      {/* One-time reveal of a freshly created key. */}
      <Modal
        open={!!created}
        onClose={() => setCreated(null)}
        title={t("api.keyCreated")}
      >
        <p className="text-sm text-ink-muted">{t("api.keyCreatedHint")}</p>
        {created?.raw_key && (
          <div className="mt-3">
            <Code block copy>
              {created.raw_key}
            </Code>
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
