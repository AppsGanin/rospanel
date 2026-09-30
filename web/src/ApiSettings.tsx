import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type ApiKey,
  type ApiKeysInfo,
  createApiKey,
  getApiKeys,
  revokeApiKey,
  setApiKeyAccess,
  setApiPath,
} from "./api";
import { fmtStamp } from "./format";
import { useShowMore } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { RouteGrid } from "./RouteGrid";
import { useCan } from "./role";
import {
  Button,
  CenterLoader,
  cn,
  Code,
  EmptyState,
  IconButton,
  IconClose,
  IconExternal,
  IconPencil,
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
import { WebhooksSettings } from "./WebhooksSettings";

// The key roster's columns, the same shape every other list in the panel has.
const TPL =
  "minmax(0,1.4fr) minmax(0,1fr) minmax(0,1fr) minmax(0,.7fr) 72px";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 560;

// KeyRow is one API key: what it is called, what it starts with, when it was minted
// and last used, and whether it still works.
function KeyRow({
  k,
  access,
  canManage,
  wide,
  onEdit,
  onRevoke,
}: {
  k: ApiKey;
  access: string;
  // Only a key the caller could have issued — the server refuses the rest.
  canManage: boolean;
  wide: boolean;
  onEdit: (k: ApiKey) => void;
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
  const action = revoked || !canManage ? null : (
    <span className="flex gap-0.5">
      <IconButton title={t("api.editPerms")} onClick={() => onEdit(k)}>
        <IconPencil size={16} />
      </IconButton>
      <IconButton color="red" title={t("api.revoke")} onClick={() => onRevoke(k)}>
        <IconClose size={16} />
      </IconButton>
    </span>
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
        <span className="truncate text-[11px] text-ink-muted">{access}</span>
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

// The tab holds two permissions: the keys and the API's address (api.manage) and the
// webhooks (webhooks.manage). A role with only the second sees only the webhooks.
export function ApiSettings() {
  const canApi = useCan("api.manage");
  const canWebhooks = useCan("webhooks.manage");
  if (!canApi) return canWebhooks ? <WebhooksSettings /> : null;
  return <ApiKeysSettings withWebhooks={canWebhooks} />;
}

function ApiKeysSettings({ withWebhooks }: { withWebhooks: boolean }) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<ApiKeysInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  // The key whose methods are being ticked: id null is a new one. A key is always
  // given methods; "everything available" is the broadest, and an old full-access
  // key opens with every method ticked and is saved as that list.
  const [editor, setEditor] = useState<{
    id: number | null;
    name: string;
    routes: Set<string>;
  } | null>(null);
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

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    refresh();
  }, []);

  // Sync the toggle draft whenever the server's enabled state changes (initial
  // load, after Save, or after rotate) — but not on a purely local flip.
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed on the server's enabled flag alone — a purely local flip of the draft must not be overwritten by the object it came from
  useEffect(() => {
    if (info) setEnabledDraft(info.enabled);
  }, [info?.enabled]);

  const save = async () => {
    if (!editor) return;
    const n = name.trim();
    if (editor.id === null && !n) return;
    setCreating(true);
    try {
      const routes = [...editor.routes];
      if (editor.id === null) {
        const res = await createApiKey(n, false, routes);
        setCreated(res.key);
        setName("");
      } else {
        await setApiKeyAccess(editor.id, false, routes);
        notifySuccess(t("api.permsSaved"));
      }
      setEditor(null);
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

  // What a key may do, in a word. Whether the caller may change or revoke it — only a
  // key they could have issued — is the server's to say (can_manage).
  const keyAccess = (k: ApiKey) =>
    k.full_access ? t("api.fullAccess") : t("api.permsCount", { count: k.routes.length });

  const enabledDirty = enabledDraft !== info.enabled;

  return (
    <div className="flex flex-1 flex-col gap-3.5">
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
              // Nothing ticked: a key is given what it needs, not everything by default.
              setEditor({ id: null, name: "", routes: new Set() });
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
              <KeyRow
                key={k.id}
                k={k}
                access={keyAccess(k)}
                canManage={!!k.can_manage}
                wide={wideKeys}
                onEdit={(k) =>
                  setEditor({ id: k.id, name: k.name, routes: new Set(k.routes) })
                }
                onRevoke={revoke}
              />
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

      {/* A key: a name and what it may do, then the one look anyone gets at it. The
          same dialog changes what an existing key may do. */}
      <Modal
        open={!!editor}
        onClose={() => setEditor(null)}
        size="lg"
        title={
          editor?.id != null ? t("api.keyPermsOf", { name: editor.name }) : t("api.keys")
        }
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="outline" color="gray" size="sm" onClick={() => setEditor(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              onClick={save}
              loading={creating}
              disabled={
                !editor ||
                (editor.id === null && !name.trim()) ||
                editor.routes.size === 0
              }
            >
              {editor?.id != null ? t("common.save") : t("common.create")}
            </Button>
          </div>
        }
      >
        {editor && (
          <div className="flex flex-col gap-3">
            {editor.id === null && (
              <TextInput
                label={t("api.newKeyName")}
                value={name}
                onChange={setName}
                placeholder={t("api.newKeyPlaceholder")}
                autoFocus
              />
            )}
            <p className="text-xs text-ink-muted">{t("api.keyPermsHint")}</p>
            <RouteGrid
              routes={info.routes}
              selected={editor.routes}
              onChange={(routes) => setEditor({ ...editor, routes })}
            />
          </div>
        )}
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

      {/* Webhooks share the tab, and sit inside this section rather than beside it:
          the save bar sticks to the bottom only within the block it is the last child
          of, so beside it the bar hung between the keys and the webhooks. */}
      {withWebhooks && <WebhooksSettings />}

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
