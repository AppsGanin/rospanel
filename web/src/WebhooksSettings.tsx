import { useEffect, useState } from "react";
import {
  createWebhook,
  deleteWebhook,
  getWebhooks,
  testWebhook,
  updateWebhook,
  type Webhook,
  type WebhookEventDef,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  Checkbox,
  IconCopy,
  SettingCard,
  Switch,
  TextInput,
  useConfirm,
  useCopy,
} from "./ui";

function fmtTs(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

// StatusBadge shows the outcome of the last delivery attempt.
function StatusBadge({ hook }: { hook: Webhook }) {
  if (!hook.last_attempt_at) return <Badge color="gray">нет доставок</Badge>;
  if (hook.last_status >= 200 && hook.last_status < 300)
    return <Badge color="green">{hook.last_status}</Badge>;
  return <Badge color="red">{hook.last_status || "ошибка"}</Badge>;
}

// SecretField reveals + copies the signing secret (needed by the receiver to
// verify the HMAC signature).
function SecretField({ value }: { value: string }) {
  const [shown, setShown] = useState(false);
  const { copied, copy } = useCopy();
  return (
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate rounded-md border border-gray-200 bg-gray-50 px-2 py-1 font-mono text-xs text-ink">
        {shown ? value : "•".repeat(24)}
      </code>
      <Button size="sm" variant="light" color="gray" onClick={() => setShown((s) => !s)}>
        {shown ? "Скрыть" : "Показать"}
      </Button>
      <Button size="sm" variant="light" color="gray" onClick={() => copy(value)}>
        <IconCopy size={14} /> {copied ? "Ок" : "Копировать"}
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
  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
      {catalog.map((e) => (
        <Checkbox
          key={e.key}
          label={e.label}
          hint={e.key}
          checked={selected.has(e.key)}
          onChange={() => onToggle(e.key)}
        />
      ))}
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
  const [events, setEvents] = useState<Set<string>>(new Set(hook.events));
  const [busy, setBusy] = useState(false);
  const [testResult, setTestResult] = useState<string>("");
  const { confirm, confirmNode } = useConfirm();

  const dirty =
    events.size !== hook.events.length ||
    hook.events.some((e) => !events.has(e));

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
      notifySuccess("События обновлены");
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
        r.ok ? `Доставлено (HTTP ${r.status})` : `Ошибка: ${r.error || r.status}`,
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
        title: "Удалить вебхук?",
        body: `Панель перестанет отправлять события на ${hook.url}.`,
        confirmLabel: "Удалить",
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
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <code className="truncate font-mono text-sm text-ink">{hook.url}</code>
            <StatusBadge hook={hook} />
          </div>
          <p className="mt-0.5 text-xs text-ink-muted">
            Последняя доставка: {fmtTs(hook.last_attempt_at)}
            {hook.last_error ? ` · ${hook.last_error}` : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Switch checked={hook.enabled} onChange={setEnabled} disabled={busy} />
        </div>
      </div>

      <div>
        <div className="mb-1 text-xs font-semibold text-ink-muted">
          Секрет подписи (HMAC-SHA256)
        </div>
        <SecretField value={hook.secret} />
      </div>

      <div>
        <div className="mb-1.5 text-xs font-semibold text-ink-muted">
          События (ничего не выбрано = все)
        </div>
        <EventPicker catalog={catalog} selected={events} onToggle={toggle} />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {dirty && (
          <Button size="sm" onClick={saveEvents} loading={busy}>
            Сохранить события
          </Button>
        )}
        <Button size="sm" variant="light" color="gray" onClick={runTest} loading={busy}>
          Тест
        </Button>
        <Button size="sm" variant="light" color="red" onClick={remove}>
          Удалить
        </Button>
        {testResult && <span className="text-xs text-ink-muted">{testResult}</span>}
      </div>
      {confirmNode}
    </Card>
  );
}

export function WebhooksSettings() {
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
      notifySuccess("Вебхук добавлен");
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
        title="Вебхуки"
        description="Панель отправляет POST-запрос на указанные адреса при событиях (создание пользователя, оплата и т.д.). Каждая доставка подписана HMAC-SHA256 — проверяйте заголовок X-RosPanel-Signature вашим секретом."
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label="URL нового вебхука"
            value={url}
            onChange={setUrl}
            placeholder="https://ваш-сервис.example.com/webhook"
          />
          <div>
            <div className="mb-1.5 text-xs font-semibold text-ink-muted">
              События (ничего не выбрано = все)
            </div>
            <EventPicker catalog={catalog} selected={newEvents} onToggle={toggleNew} />
          </div>
          <div>
            <Button onClick={create} loading={creating} disabled={!url.trim()}>
              Добавить вебхук
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
