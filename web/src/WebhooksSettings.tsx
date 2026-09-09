import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createWebhook,
  deleteWebhook,
  getWebhooks,
  testWebhook,
  updateWebhook,
  type Webhook,
  type WebhookEventDef,
} from "./api";
import { fmtStamp } from "./format";
import i18n, { slugKey, td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  Code,
  cn,
  EmptyState,
  IconButton,
  IconPencil,
  IconPlus,
  IconTrash,
  MICRO,
  Modal,
  Mono,
  Panel,
  SettingRow,
  Switch,
  TextInput,
  useConfirm,
  useWideBox,
} from "./ui";

// The outcome of the last attempt, as part of the line that already says when it
// was — a badge beside the URL made a row of two headline elements out of one.
function lastDelivery(hook: Webhook): { text: string; failed: boolean } {
  if (!hook.last_attempt_at) return { text: i18n.t("hooks.noDeliveries"), failed: false };
  const ok = hook.last_status >= 200 && hook.last_status < 300;
  const parts = [
    i18n.t("hooks.lastDelivery", { when: fmtStamp(hook.last_attempt_at) }),
    String(hook.last_status || i18n.t("hooks.failed")),
    hook.last_error || "",
  ].filter(Boolean);
  return { text: parts.join(" · "), failed: !ok };
}

// EventPicker is the chip grid for choosing subscribed events (none ticked = all
// events). Chips rather than a column of boxes: what the operator does here is
// glance at which ones are lit.
function EventPicker({
  catalog,
  selected,
  onToggle,
}: {
  catalog: WebhookEventDef[];
  selected: Set<string>;
  onToggle: (key: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {catalog.map((e) => {
        const on = selected.has(e.key);
        return (
          <label
            key={e.key}
            title={e.key}
            className={cn(
              // relative: the sr-only input inside is absolutely positioned, and
              // without an anchor the browser scrolls the page to wherever it lands.
              "relative cursor-pointer select-none rounded-md border px-2 py-1 text-[11px] transition",
              on
                ? "accent-tint border-accent text-accent"
                : "border-gray-300 text-ink-muted hover:border-gray-400",
            )}
          >
            <input
              type="checkbox"
              className="sr-only"
              checked={on}
              onChange={() => onToggle(e.key)}
            />
            {td(`webhookEvent.${slugKey(e.key)}`)}
          </label>
        );
      })}
    </div>
  );
}

// One line per endpoint: where it posts, how the last attempt went, whether it is
// on, and the two things one does to it. Everything else about a hook — its secret
// and its event list — is a dialog away, because it is read once and then left.
const TPL = "minmax(0,1.6fr) minmax(0,1.2fr) 36px 32px 32px";
const TPL_NARROW = "minmax(0,1fr) 36px 32px 32px";
const WIDE_MIN = 620;

function HookRow({
  hook,
  wide,
  busy,
  onToggle,
  onEdit,
  onDelete,
}: {
  hook: Webhook;
  wide: boolean;
  busy: boolean;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const delivery = lastDelivery(hook);
  const when = (
    <span
      className={cn(
        "truncate text-[11px]",
        delivery.failed ? "text-danger" : "text-ink-muted",
      )}
      title={delivery.text}
    >
      {delivery.text}
    </span>
  );
  return (
    <div
      className="grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]"
      style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
    >
      <Mono className="truncate text-xs text-ink" title={hook.url}>
        {hook.url}
      </Mono>
      {wide && when}
      <Switch checked={hook.enabled} onChange={onToggle} disabled={busy} />
      <IconButton title={t("common.edit")} onClick={onEdit}>
        <IconPencil />
      </IconButton>
      <IconButton color="red" title={t("common.delete")} onClick={onDelete}>
        <IconTrash />
      </IconButton>
      {!wide && <span className="col-span-4 min-w-0">{when}</span>}
    </div>
  );
}

export function WebhooksSettings() {
  const { t } = useTranslation();
  const [webhooks, setWebhooks] = useState<Webhook[]>([]);
  const [catalog, setCatalog] = useState<WebhookEventDef[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  // The add dialog, and the one that edits an existing hook. Both hold a URL and a
  // set of events; only the second has a secret to show and a delivery to test.
  const [adding, setAdding] = useState(false);
  const [url, setUrl] = useState("");
  const [draft, setDraft] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<Webhook | null>(null);
  const [testResult, setTestResult] = useState("");
  const [secretShown, setSecretShown] = useState(false);
  const { confirm, confirmNode } = useConfirm();
  const [listRef, wide] = useWideBox(WIDE_MIN);

  const refresh = () =>
    getWebhooks()
      .then((info) => {
        setWebhooks(info.webhooks);
        setCatalog(info.events);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));

  useEffect(() => {
    refresh();
  }, []);

  const toggleDraft = (key: string) =>
    setDraft((prev) => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });

  const openAdd = () => {
    setUrl("");
    setDraft(new Set());
    setAdding(true);
  };

  // null events = every event; the picker holds the explicit set either way.
  const openEdit = (hook: Webhook) => {
    setDraft(new Set(hook.events ?? []));
    setTestResult("");
    setSecretShown(false);
    setEditing(hook);
  };

  const create = async () => {
    const u = url.trim();
    if (!u) return;
    setBusy(true);
    try {
      await createWebhook(u, [...draft]);
      setAdding(false);
      await refresh();
      notifySuccess(t("hooks.added"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const setEnabled = async (hook: Webhook, enabled: boolean) => {
    setBusy(true);
    try {
      await updateWebhook(hook.id, hook.url, hook.events ?? [], enabled);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const saveEvents = async () => {
    if (!editing) return;
    setBusy(true);
    try {
      await updateWebhook(editing.id, editing.url, [...draft], editing.enabled);
      notifySuccess(t("hooks.eventsUpdated"));
      setEditing(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const runTest = async () => {
    if (!editing) return;
    setBusy(true);
    setTestResult("");
    try {
      const r = await testWebhook(editing.id);
      setTestResult(
        r.ok
          ? t("hooks.delivered", { status: r.status })
          : t("hooks.deliverFailed", { error: r.error || r.status }),
      );
      await refresh();
    } catch (e) {
      setTestResult(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (hook: Webhook) => {
    if (
      !(await confirm({
        title: t("hooks.deleteTitle"),
        body: t("hooks.deleteBody", { url: hook.url }),
        confirmLabel: t("common.delete"),
        danger: true,
      }))
    )
      return;
    try {
      await deleteWebhook(hook.id);
      setEditing(null);
      await refresh();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // No standalone loader here: this section renders under <ApiSettings/> in the
  // same tab, and that component already shows one CenterLoader while loading —
  // a second one here would show two spinners at once.
  if (loading) return null;

  const picker = (
    <div>
      <div className={cn(MICRO, "mb-1.5")}>{t("hooks.eventsLabel")}</div>
      <EventPicker catalog={catalog} selected={draft} onToggle={toggleDraft} />
    </div>
  );

  return (
    <>
      <Panel
        title={t("hooks.title")}
        aside={
          <IconButton
            variant="filled"
            color="brand"
            title={t("hooks.add")}
            onClick={openAdd}
          >
            <IconPlus />
          </IconButton>
        }
      >
        <SettingRow hint={t("hooks.description")} />
        {webhooks.length === 0 ? (
          <EmptyState title={t("hooks.noHooks")} />
        ) : (
          <div ref={listRef}>
            {webhooks.map((h) => (
              <HookRow
                key={h.id}
                hook={h}
                wide={wide}
                busy={busy}
                onToggle={(v) => setEnabled(h, v)}
                onEdit={() => openEdit(h)}
                onDelete={() => remove(h)}
              />
            ))}
          </div>
        )}
      </Panel>

      {/* A new endpoint: where to post, and what to post there. */}
      <Modal
        open={adding}
        onClose={() => setAdding(false)}
        title={t("hooks.add")}
        footer={
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              color="gray"
              size="sm"
              onClick={() => setAdding(false)}
            >
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={create} loading={busy} disabled={!url.trim()}>
              {t("common.add")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("hooks.newUrl")}
            value={url}
            onChange={setUrl}
            placeholder="https://your-service.example.com/webhook"
            autoFocus
          />
          {picker}
        </div>
      </Modal>

      {/* An existing one: the events it takes, the secret that signs them, and a
          delivery one can fire by hand to see the receiver answer. */}
      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={t("hooks.title")}
        subtitle={editing?.url}
        footer={
          <div className="flex flex-wrap items-center justify-end gap-2">
            {testResult && (
              <span className="mr-auto text-[11px] text-ink-muted">{testResult}</span>
            )}
            <Button
              size="sm"
              variant="outline"
              color="gray"
              onClick={runTest}
              loading={busy}
            >
              {t("hooks.test")}
            </Button>
            <Button size="sm" onClick={saveEvents} loading={busy}>
              {t("common.save")}
            </Button>
          </div>
        }
      >
        {editing && (
          <div className="flex flex-col gap-3">
            {picker}
            <div>
              <div className={cn(MICRO, "mb-1")}>{t("hooks.signingSecret")}</div>
              <Code block copy>
                {secretShown ? editing.secret : "•".repeat(24)}
              </Code>
              <button
                type="button"
                onClick={() => setSecretShown((s) => !s)}
                className="mt-1 text-[11px] text-ink-muted underline-offset-2 transition hover:text-accent hover:underline"
              >
                {t(secretShown ? "hooks.hide" : "hooks.show")}
              </button>
            </div>
          </div>
        )}
      </Modal>

      {confirmNode}
    </>
  );
}
