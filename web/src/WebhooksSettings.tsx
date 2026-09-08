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
  cn,
  IconCopy,
  MICRO,
  Mono,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
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

// SecretField reveals + copies the signing secret (needed by the receiver to
// verify the HMAC signature).
function SecretField({ value }: { value: string }) {
  const [shown, setShown] = useState(false);
  const { copied, copy } = useCopy();
  return (
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate rounded-md border border-gray-200 bg-gray-50 px-2 py-1 font-mono text-[11px] text-ink">
        {shown ? value : "•".repeat(24)}
      </code>
      <Button size="xs" variant="outline" color="gray" onClick={() => setShown((s) => !s)}>
        {i18n.t(shown ? "hooks.hide" : "hooks.show")}
      </Button>
      <Button size="xs" variant="outline" color="gray" onClick={() => copy(value)}>
        <IconCopy size={14} /> {i18n.t(copied ? "hooks.ok" : "common.copy")}
      </Button>
    </div>
  );
}

// EventPicker is the checkbox grid for choosing subscribed events (none ticked =
// all events).
function EventPicker({
  catalog,
  selected,
  onToggle,
}: {
  catalog: WebhookEventDef[];
  selected: Set<string>;
  onToggle: (key: string) => void;
}) {
  // Chips, not a column of boxed checkboxes: nine events took half a screen each
  // time, and what the operator does here is glance at which ones are lit.
  return (
    <div className="flex flex-wrap gap-1.5">
      {catalog.map((e) => {
        const on = selected.has(e.key);
        return (
          <label
            key={e.key}
            title={e.key}
            className={cn(
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

// WebhookRow is one configured endpoint with inline edit of its events + enabled
// flag.
function WebhookRow({
  hook,
  catalog,
  onChanged,
}: {
  hook: Webhook;
  catalog: WebhookEventDef[];
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  // null events = every event; the picker holds the explicit set either way.
  const hookEvents = hook.events ?? [];
  const [events, setEvents] = useState<Set<string>>(new Set(hookEvents));
  const [busy, setBusy] = useState(false);
  const [testResult, setTestResult] = useState<string>("");
  const { confirm, confirmNode } = useConfirm();
  const delivery = lastDelivery(hook);

  const dirty =
    events.size !== hookEvents.length || hookEvents.some((e) => !events.has(e));

  const toggle = (key: string) =>
    setEvents((prev) => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });

  const setEnabled = async (enabled: boolean) => {
    setBusy(true);
    try {
      await updateWebhook(hook.id, hook.url, [...events], enabled);
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const saveEvents = async () => {
    setBusy(true);
    try {
      await updateWebhook(hook.id, hook.url, [...events], hook.enabled);
      notifySuccess(t("hooks.eventsUpdated"));
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const runTest = async () => {
    setBusy(true);
    setTestResult("");
    try {
      const r = await testWebhook(hook.id);
      setTestResult(
        r.ok
          ? t("hooks.delivered", { status: r.status })
          : t("hooks.deliverFailed", { error: r.error || r.status }),
      );
      onChanged();
    } catch (e) {
      setTestResult(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
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
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-gray-200 p-2.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <Mono className="truncate text-xs text-ink">{hook.url}</Mono>
          <p
            className={cn(
              "mt-0.5 truncate text-[11px]",
              delivery.failed ? "text-danger" : "text-ink-muted",
            )}
            title={delivery.text}
          >
            {delivery.text}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Switch checked={hook.enabled} onChange={setEnabled} disabled={busy} />
        </div>
      </div>

      <div>
        <div className={cn(MICRO, "mb-1")}>{t("hooks.signingSecret")}</div>
        <SecretField value={hook.secret} />
      </div>

      <div>
        <div className={cn(MICRO, "mb-1.5")}>{t("hooks.eventsLabel")}</div>
        <EventPicker catalog={catalog} selected={events} onToggle={toggle} />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {dirty && (
          <Button size="xs" onClick={saveEvents} loading={busy}>
            {t("hooks.saveEvents")}
          </Button>
        )}
        <Button size="xs" variant="outline" color="gray" onClick={runTest} loading={busy}>
          {t("hooks.test")}
        </Button>
        <Button size="xs" variant="outline" color="red" onClick={remove}>
          {t("common.delete")}
        </Button>
        {testResult && <span className="text-[11px] text-ink-muted">{testResult}</span>}
      </div>
      {confirmNode}
    </div>
  );
}

export function WebhooksSettings() {
  const { t } = useTranslation();
  const [webhooks, setWebhooks] = useState<Webhook[]>([]);
  const [catalog, setCatalog] = useState<WebhookEventDef[]>([]);
  const [loading, setLoading] = useState(true);
  const [url, setUrl] = useState("");
  const [newEvents, setNewEvents] = useState<Set<string>>(new Set());
  const [creating, setCreating] = useState(false);

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

  const create = async () => {
    const u = url.trim();
    if (!u) return;
    setCreating(true);
    try {
      await createWebhook(u, [...newEvents]);
      setUrl("");
      setNewEvents(new Set());
      await refresh();
      notifySuccess(t("hooks.added"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setCreating(false);
    }
  };

  const toggleNew = (key: string) =>
    setNewEvents((prev) => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });

  // No standalone loader here: this section renders under <ApiSettings/> in the
  // same tab, and that component already shows one CenterLoader while loading —
  // a second one here would show two spinners at once.
  if (loading) return null;

  return (
    <div className="flex flex-col gap-4">
      <SettingCard
        title={t("hooks.title")}
        description={t("hooks.description")}
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label={t("hooks.newUrl")}
            value={url}
            onChange={setUrl}
            placeholder="https://your-service.example.com/webhook"
          />
          <div>
            <div className="mb-1.5 text-xs font-semibold text-ink-muted">
              {t("hooks.eventsLabel")}
            </div>
            <EventPicker catalog={catalog} selected={newEvents} onToggle={toggleNew} />
          </div>
          <div>
            <Button onClick={create} loading={creating} disabled={!url.trim()}>
              {t("hooks.add")}
            </Button>
          </div>
        </div>
      </SettingCard>

      {webhooks.map((h) => (
        <WebhookRow key={h.id} hook={h} catalog={catalog} onChanged={refresh} />
      ))}
    </div>
  );
}
