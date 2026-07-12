import { useCallback, useEffect, useState } from "react";
import {
  type AdminAudit,
  getAdminAuditCatalog,
  listAdminAudit,
} from "./api";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, Select, SettingCard, Skeleton } from "./ui";

const PAGE = 50;

function fmtTs(unix: number): string {
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

// Rows the owner should be able to spot at a glance: a failed sign-in, and the two
// actions that are irreversible.
function toneOf(action: string): "danger" | "warn" | "plain" {
  if (action === "admin.login_failed") return "warn";
  if (
    action === "admin.deleted" ||
    action === "panel.factory_reset" ||
    action === "panel.restored"
  ) {
    return "danger";
  }
  return "plain";
}

// details is a small JSON object ({"role":"operator"}, {"from":…,"to":…}) — render it
// as plain "key: value" pairs rather than dumping JSON at the reader.
function fmtDetails(d: AdminAudit["details"]): string {
  if (!d || typeof d !== "object") return "";
  return Object.entries(d)
    .map(([k, v]) => `${k}: ${String(v)}`)
    .join(" · ");
}

function AuditRow({
  ev,
  label,
}: {
  ev: AdminAudit;
  label: string;
}) {
  const tone = toneOf(ev.action);
  const details = fmtDetails(ev.details);
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-gray-200 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {tone === "danger" ? (
            <Badge color="red">{label}</Badge>
          ) : tone === "warn" ? (
            <Badge color="orange">{label}</Badge>
          ) : (
            <span className="font-semibold text-ink">{label}</span>
          )}
          {ev.target && (
            <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-ink-muted">
              {ev.target}
            </code>
          )}
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          {ev.actor_name || "—"}
          {ev.ip && ` · ${ev.ip}`}
          {details && ` · ${details}`}
        </div>
      </div>
      <div className="shrink-0 text-xs text-ink-muted sm:text-right">
        {fmtTs(ev.created_at)}
      </div>
    </div>
  );
}

export function AdminAuditPanel() {
  const [events, setEvents] = useState<AdminAudit[]>([]);
  const [labels, setLabels] = useState<Record<string, string>>({});
  const [options, setOptions] = useState<{ value: string; label: string }[]>([
    { value: "", label: "Все события" },
  ]);
  const [category, setCategory] = useState("");
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);

  useEffect(() => {
    getAdminAuditCatalog()
      .then((c) => {
        // Rows are titled by their exact action; the filter offers areas.
        setLabels(Object.fromEntries(c.actions.map((a) => [a.key, a.label])));
        setOptions([
          { value: "", label: "Все события" },
          ...c.categories.map((x) => ({ value: x.key, label: x.label })),
        ]);
      })
      .catch(() => {}); // the journal still renders, just with raw action keys
  }, []);

  // Refetch from the top whenever the filter changes.
  const load = useCallback(() => {
    setLoading(true);
    listAdminAudit({ category, limit: PAGE })
      .then((p) => {
        setEvents(p.events);
        setNext(p.next_before);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoading(false));
  }, [category]);

  useEffect(() => {
    load();
  }, [load]);

  const loadMore = () => {
    if (!next) return;
    setMore(true);
    listAdminAudit({ category, before: next, limit: PAGE })
      .then((p) => {
        setEvents((prev) => [...prev, ...p.events]);
        setNext(p.next_before);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setMore(false));
  };

  return (
    <SettingCard
      title="Журнал администраторов"
      description="Кто входил в панель и что менял: настройки, тарифы, ключи, бэкапы, состав администраторов. Пароли и токены в журнал не попадают. Хранится 90 дней."
      action={
        <div className="w-48">
          <Select value={category} onChange={setCategory} data={options} />
        </div>
      }
      stackAction
    >
      {loading ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      ) : events.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-muted">
          Событий нет.
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {events.map((ev) => (
            <AuditRow
              key={ev.id}
              ev={ev}
              label={labels[ev.action] ?? ev.action}
            />
          ))}
          {next > 0 && (
            <div className="mt-2 flex justify-center">
              <Button
                variant="light"
                color="gray"
                loading={more}
                onClick={loadMore}
              >
                Показать ещё
              </Button>
            </div>
          )}
        </div>
      )}
    </SettingCard>
  );
}
