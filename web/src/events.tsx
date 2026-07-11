import { useCallback, useEffect, useRef, useState } from "react";
import type { EventPage, UserEvent } from "./api";
import { fmtBytes } from "./format";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, CenterLoader } from "./ui";

// The audit-log rendering shared by the per-user «Журнал» modal and the global
// journal page: how each action is labelled and coloured, how its details read in
// Russian, and the paged list itself.

type Color = "brand" | "green" | "orange" | "red" | "gray" | "teal";

// ACTION_META mirrors model.UserEventCatalog (internal/model/events.go). An action
// missing here still renders — it just falls back to its raw key.
const ACTION_META: Record<string, { label: string; color: Color }> = {
  "user.created": { label: "Пользователь создан", color: "green" },
  "user.registered": { label: "Саморегистрация", color: "green" },
  "user.deleted": { label: "Пользователь удалён", color: "red" },
  "user.renamed": { label: "Переименован", color: "gray" },
  "user.enabled": { label: "Включён", color: "green" },
  "user.disabled": { label: "Отключён", color: "orange" },
  "user.limits_changed": { label: "Изменены лимиты", color: "brand" },
  "user.traffic_reset": { label: "Сброшен трафик", color: "brand" },
  "user.quota_reset": { label: "Автосброс квоты", color: "gray" },
  "user.reset_period": { label: "Период автосброса", color: "gray" },
  "user.sub_rotated": { label: "Обновлена ссылка", color: "brand" },
  "user.expired": { label: "Подписка истекла", color: "orange" },
  "user.limited": { label: "Исчерпан трафик", color: "orange" },
  "user.device_limited": { label: "Лимит устройств", color: "orange" },
  "user.telegram_linked": { label: "Telegram привязан", color: "teal" },
  "user.telegram_unlinked": { label: "Telegram отвязан", color: "gray" },
  "plan.changed": { label: "Изменён тариф", color: "brand" },
  "plan.downgraded": { label: "Переведён на бесплатный", color: "orange" },
  "plan.cancelled": { label: "Подписка отменена", color: "orange" },
  "payment.created": { label: "Заказ создан", color: "brand" },
  "payment.paid": { label: "Оплачено", color: "green" },
  "payment.cancelled": { label: "Заказ отменён", color: "gray" },
};

export function actionMeta(action: string) {
  return ACTION_META[action] ?? { label: action, color: "gray" as Color };
}

// ACTOR_META mirrors the model.Actor* kinds.
const ACTOR_META: Record<string, string> = {
  admin: "админ",
  apikey: "API-ключ",
  telegram: "Telegram",
  user: "сам пользователь",
  system: "система",
};

export const ACTOR_OPTIONS = [
  { value: "", label: "Кто угодно" },
  { value: "admin", label: "Админ" },
  { value: "apikey", label: "API-ключ" },
  { value: "telegram", label: "Telegram-бот" },
  { value: "user", label: "Сам пользователь" },
  { value: "system", label: "Система" },
];

// actorLabel reads as "кто это сделал": the person's name when we have one,
// otherwise just the kind ("система" has no name).
function actorLabel(e: UserEvent): string {
  const kind = ACTOR_META[e.actor_kind] ?? e.actor_kind;
  return e.actor_name ? `${e.actor_name} · ${kind}` : kind;
}

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtDate(unix: number): string {
  if (!unix) return "бессрочно";
  return new Date(unix * 1000).toLocaleDateString("ru-RU");
}

// num/str read one typed field out of the free-form details object. A missing key
// reads as 0/"" — so a renderer must distinguish "absent" from "zero" (has() does)
// before turning a value into a claim like "без лимита".
function num(d: Record<string, unknown>, k: string): number {
  const v = d[k];
  return typeof v === "number" ? v : 0;
}
function has(d: Record<string, unknown>, k: string): boolean {
  return d[k] !== undefined && d[k] !== null;
}
function str(d: Record<string, unknown>, k: string): string {
  const v = d[k];
  return typeof v === "string" ? v : "";
}

const PERIOD_LABELS: Record<string, string> = {
  none: "без автосброса",
  daily: "ежедневно",
  weekly: "еженедельно",
  monthly: "ежемесячно",
  yearly: "ежегодно",
};

const PROVIDER_LABELS: Record<string, string> = {
  manual: "вручную",
  yookassa: "ЮКасса",
  cryptobot: "CryptoBot",
};

const CANCEL_REASONS: Record<string, string> = {
  abandoned: "истёк срок оплаты",
  provider_cancelled: "отменён провайдером",
};

// eventDetails turns the row's details object into the one-line human summary shown
// under the action name. An action with nothing worth saying returns "".
export function eventDetails(e: UserEvent): string {
  const d = e.details;
  if (!d) return "";
  const parts: string[] = [];
  switch (e.action) {
    case "user.created":
    case "user.limits_changed": {
      // Only state a limit the row actually carries — a missing key is "unknown",
      // not "unlimited", and rendering it as the latter would be a false claim.
      if (has(d, "data_limit")) {
        const limit = num(d, "data_limit");
        parts.push(limit ? `лимит ${fmtBytes(limit)}` : "без лимита трафика");
      }
      if (has(d, "expire_at")) {
        const expire = num(d, "expire_at");
        parts.push(expire ? `до ${fmtDate(expire)}` : "бессрочно");
      }
      const devices = num(d, "device_limit");
      if (devices) parts.push(`устройств: ${devices}`);
      const days = num(d, "extended_days");
      if (days) parts.push(`продлено на ${days} дн.`);
      break;
    }
    case "user.renamed":
      return `${str(d, "from") || "—"} → ${str(d, "to")}`;
    case "user.traffic_reset":
    case "user.quota_reset": {
      const used = num(d, "used_before");
      if (used) parts.push(`сброшено ${fmtBytes(used)}`);
      const period = str(d, "period");
      if (period && period !== "none")
        parts.push(PERIOD_LABELS[period] ?? period);
      break;
    }
    case "user.reset_period":
      return PERIOD_LABELS[str(d, "period")] ?? str(d, "period");
    case "user.registered":
      return str(d, "plan") ? `тариф: ${str(d, "plan")}` : "";
    case "user.expired":
      return `срок истёк ${fmtDate(num(d, "expire_at"))}`;
    case "user.limited":
      return `${fmtBytes(num(d, "used"))} из ${fmtBytes(num(d, "data_limit"))}`;
    case "user.device_limited":
      return `${num(d, "active_devices")} устройств при лимите ${num(d, "device_limit")}`;
    case "user.telegram_linked":
      return str(d, "username");
    case "plan.changed":
    case "plan.downgraded": {
      const prev = str(d, "prev_plan") || "без тарифа";
      const next = str(d, "plan") || "без тарифа";
      parts.push(`${prev} → ${next}`);
      const expire = num(d, "expire_at");
      if (expire) parts.push(`до ${fmtDate(expire)}`);
      break;
    }
    case "plan.cancelled": {
      const plan = str(d, "plan");
      if (plan) parts.push(plan);
      const to = str(d, "moved_to");
      if (to) parts.push(`переведён на «${to}»`);
      break;
    }
    case "payment.created":
    case "payment.paid":
    case "payment.cancelled": {
      const order = num(d, "order_id");
      if (order) parts.push(`заказ #${order}`);
      const plan = str(d, "plan");
      if (plan) parts.push(plan);
      const amount = num(d, "amount_rub");
      if (amount) parts.push(`${amount.toLocaleString("ru-RU")} ₽`);
      const provider = str(d, "provider");
      if (provider) parts.push(PROVIDER_LABELS[provider] ?? provider);
      const reason = str(d, "reason");
      if (reason) parts.push(CANCEL_REASONS[reason] ?? reason);
      break;
    }
  }
  return parts.join(" · ");
}

// EventRow is one entry in the trail. showUser adds the affected user's name — the
// global journal needs it, the per-user modal already knows who it's about.
export function EventRow({
  event,
  showUser,
}: {
  event: UserEvent;
  showUser?: boolean;
}) {
  const meta = actionMeta(event.action);
  const details = eventDetails(event);
  const bulk = event.details?.bulk === true;
  return (
    <li className="flex flex-col gap-1 rounded-lg border border-gray-100 bg-gray-50/80 px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <Badge color={meta.color} size="xs">
          {meta.label}
        </Badge>
        {bulk && (
          <Badge color="gray" size="xs">
            массово
          </Badge>
        )}
        {showUser && (
          <span className="truncate text-sm font-medium text-ink">
            {event.user_name || `#${event.user_id}`}
          </span>
        )}
      </div>
      {details && <div className="text-sm text-ink">{details}</div>}
      <div className="text-xs text-ink-muted">
        {actorLabel(event)} · {fmtDateTime(event.created_at)}
      </div>
    </li>
  );
}

// EventList renders a paged trail. `load` fetches one page given a `before` cursor
// (0 = the newest page); it is re-run whenever the identity of `load` changes, so
// callers must memoize it (useCallback) with their filters in the dependency list.
export function EventList({
  load,
  showUser,
  empty = "Пока нет событий",
}: {
  load: (before: number) => Promise<EventPage>;
  showUser?: boolean;
  empty?: string;
}) {
  const [events, setEvents] = useState<UserEvent[]>([]);
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);
  // Guards against a stale response from a previous filter overwriting the current
  // one: only the newest request may commit its result.
  const reqID = useRef(0);

  useEffect(() => {
    const id = ++reqID.current;
    setLoading(true);
    load(0)
      .then((page) => {
        if (id !== reqID.current) return;
        setEvents(page.events);
        setNext(page.next_before);
      })
      .catch((e) => {
        if (id === reqID.current) notifyError(errMessage(e));
      })
      .finally(() => {
        if (id === reqID.current) setLoading(false);
      });
  }, [load]);

  const loadMore = useCallback(() => {
    if (!next) return;
    const id = reqID.current;
    setMore(true);
    load(next)
      .then((page) => {
        if (id !== reqID.current) return;
        setEvents((prev) => [...prev, ...page.events]);
        setNext(page.next_before);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setMore(false));
  }, [load, next]);

  if (loading) return <CenterLoader />;
  if (!events.length)
    return <div className="py-6 text-center text-sm text-ink-muted">{empty}</div>;

  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-col gap-2">
        {events.map((e) => (
          <EventRow key={e.id} event={e} showUser={showUser} />
        ))}
      </ul>
      {next > 0 && (
        <Button variant="light" fullWidth loading={more} onClick={loadMore}>
          Показать ещё
        </Button>
      )}
    </div>
  );
}
