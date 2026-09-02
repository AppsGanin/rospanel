import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { EventPage, UserEvent } from "./api";
import { fmtBytes } from "./format";
import i18n, { currentLang, slugKey, td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import { Badge, Button, CenterLoader, TableShell, TD, THead, TR } from "./ui";

// The audit-log rendering shared by the per-user journal modal and the global
// journal page: how each action is labelled and coloured, how its details read,
// and the paged list itself.

type Color = "brand" | "green" | "orange" | "red" | "gray" | "teal";

// ACTION_COLORS mirrors model.UserEventCatalog (internal/model/events.go). The
// label for each action lives in the dictionaries under events.action.<key>; an
// action missing there still renders — it just falls back to its raw key.
const ACTION_COLORS: Record<string, Color> = {
  "user.created": "green",
  "user.registered": "green",
  "user.deleted": "red",
  "user.renamed": "gray",
  "user.note_changed": "gray",
  "user.tags_changed": "gray",
  "user.enabled": "green",
  "user.disabled": "orange",
  "user.limits_changed": "brand",
  "user.traffic_reset": "brand",
  "user.quota_reset": "gray",
  "user.reset_period": "gray",
  "user.sub_rotated": "brand",
  "user.expired": "orange",
  "user.limited": "orange",
  "user.device_limited": "orange",
  "user.policy_refused": "orange",
  "user.telegram_linked": "teal",
  "user.telegram_unlinked": "gray",
  "plan.changed": "brand",
  "plan.downgraded": "orange",
  "plan.cancelled": "orange",
  "payment.created": "brand",
  "payment.paid": "green",
  "payment.cancelled": "gray",
};

export function actionMeta(action: string): { label: string; color: Color } {
  const color = ACTION_COLORS[action];
  if (!color) return { label: action, color: "gray" };
  return { label: td(`events.action.${slugKey(action)}`), color };
}

const ACTOR_KINDS = ["admin", "apikey", "telegram", "user", "system"] as const;

function actorKindLabel(kind: string): string {
  return (ACTOR_KINDS as readonly string[]).includes(kind)
    ? td(`events.actor.${kind}`)
    : kind;
}

export const actorOptions = () => [
  { value: "", label: i18n.t("events.actorAny") },
  ...ACTOR_KINDS.map((k) => ({
    value: k as string,
    label: td(`events.actorOption.${k}`),
  })),
];

// actorLabel reads as "who did this": the person's name when we have one,
// otherwise just the kind (the system has no name).
function actorLabel(e: UserEvent): string {
  const kind = actorKindLabel(e.actor_kind);
  return e.actor_name ? `${e.actor_name} · ${kind}` : kind;
}

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtDate(unix: number): string {
  if (!unix) return i18n.t("common.never");
  return new Date(unix * 1000).toLocaleDateString(currentLang());
}

// num/str read one typed field out of the free-form details object. A missing key
// reads as 0/"" — so a renderer must distinguish "absent" from "zero" (has() does)
// before turning a value into a claim like "unlimited".
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

const PERIODS = ["none", "daily", "weekly", "monthly", "yearly"] as const;

function periodLabel(p: string): string {
  return (PERIODS as readonly string[]).includes(p) ? td(`events.period.${p}`) : p;
}

const PROVIDERS = ["manual", "yookassa", "cryptobot"] as const;

function providerLabel(p: string): string {
  return (PROVIDERS as readonly string[]).includes(p)
    ? td(`events.provider.${p}`)
    : p;
}

const CANCEL_REASONS = ["abandoned", "provider_cancelled"] as const;

function cancelReason(r: string): string {
  return (CANCEL_REASONS as readonly string[]).includes(r)
    ? td(`events.cancelReason.${r}`)
    : r;
}

// eventDetails turns the row's details object into the one-line human summary shown
// under the action name. An action with nothing worth saying returns "".
export function eventDetails(e: UserEvent): string {
  const d = e.details;
  if (!d) return "";
  const parts: string[] = [];
  switch (e.action) {
    case "user.created":
    case "user.limits_changed": {
      if (str(d, "imported_from")) {
        parts.push(i18n.t("events.det.importedFrom", { source: str(d, "imported_from") }));
      }
      // Only state a limit the row actually carries — a missing key is "unknown",
      // not "unlimited", and rendering it as the latter would be a false claim.
      if (has(d, "data_limit")) {
        const limit = num(d, "data_limit");
        parts.push(
          limit
            ? i18n.t("events.det.limit", { value: fmtBytes(limit) })
            : i18n.t("events.det.noTrafficLimit"),
        );
      }
      if (has(d, "expire_at")) {
        const expire = num(d, "expire_at");
        parts.push(
          expire
            ? i18n.t("events.det.until", { date: fmtDate(expire) })
            : i18n.t("common.never"),
        );
      }
      const devices = num(d, "device_limit");
      if (devices) parts.push(i18n.t("events.det.devices", { count: devices }));
      const days = num(d, "extended_days");
      if (days) parts.push(i18n.t("events.det.extendedDays", { count: days }));
      break;
    }
    case "user.renamed":
      return `${str(d, "from") || "—"} → ${str(d, "to")}`;
    case "user.tags_changed": {
      // Both sides are lists; an empty one reads as "—" so a clear is visible.
      const list = (k: string) => {
        const v = d[k];
        return Array.isArray(v) && v.length ? v.join(", ") : "—";
      };
      return `${list("from")} → ${list("to")}`;
    }
    case "user.traffic_reset":
    case "user.quota_reset": {
      const used = num(d, "used_before");
      if (used) parts.push(i18n.t("events.det.reset", { value: fmtBytes(used) }));
      const period = str(d, "period");
      if (period && period !== "none") parts.push(periodLabel(period));
      break;
    }
    case "user.reset_period":
      return periodLabel(str(d, "period"));
    case "user.registered":
      return str(d, "plan")
        ? i18n.t("events.det.plan", { plan: str(d, "plan") })
        : "";
    case "user.expired":
      return i18n.t("events.det.expiredOn", {
        date: fmtDate(num(d, "expire_at")),
      });
    case "user.limited":
      return i18n.t("events.det.usedOf", {
        used: fmtBytes(num(d, "used")),
        limit: fmtBytes(num(d, "data_limit")),
      });
    case "user.device_limited":
      return i18n.t("events.det.devicesOverLimit", {
        active: num(d, "active_devices"),
        limit: num(d, "device_limit"),
      });
    case "user.telegram_linked":
      return str(d, "username");
    case "plan.changed":
    case "plan.downgraded": {
      const noPlan = i18n.t("events.det.noPlan");
      const prev = str(d, "prev_plan") || noPlan;
      const next = str(d, "plan") || noPlan;
      parts.push(`${prev} → ${next}`);
      const expire = num(d, "expire_at");
      if (expire) parts.push(i18n.t("events.det.until", { date: fmtDate(expire) }));
      break;
    }
    case "plan.cancelled": {
      const plan = str(d, "plan");
      if (plan) parts.push(plan);
      const to = str(d, "moved_to");
      if (to) parts.push(i18n.t("events.det.movedTo", { plan: to }));
      break;
    }
    case "payment.created":
    case "payment.paid":
    case "payment.cancelled": {
      const order = num(d, "order_id");
      if (order) parts.push(i18n.t("events.det.order", { id: order }));
      const plan = str(d, "plan");
      if (plan) parts.push(plan);
      const amount = num(d, "amount_rub");
      if (amount) parts.push(`${amount.toLocaleString(currentLang())} ₽`);
      const provider = str(d, "provider");
      if (provider) parts.push(providerLabel(provider));
      const reason = str(d, "reason");
      if (reason) parts.push(cancelReason(reason));
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
  const { t } = useTranslation();
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
            {t("events.bulk")}
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
  empty,
  table,
}: {
  load: (before: number) => Promise<EventPage>;
  showUser?: boolean;
  empty?: string;
  // table renders the trail as columns instead of stacked cards. Used by the global
  // journal, where every row has the same four facts and scanning down a column is the
  // point; the per-user trail inside a modal keeps the cards, which read better narrow.
  table?: boolean;
}) {
  const { t } = useTranslation();
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
    return (
      <div className="py-6 text-center text-sm text-ink-muted">
        {empty ?? t("events.empty")}
      </div>
    );

  return (
    <div className="flex flex-col gap-3">
      {table ? (
        <EventTable events={events} showUser={showUser} />
      ) : (
        <ul className="flex flex-col gap-2">
          {events.map((e) => (
            <EventRow key={e.id} event={e} showUser={showUser} />
          ))}
        </ul>
      )}
      {next > 0 && (
        <Button variant="light" fullWidth loading={more} onClick={loadMore}>
          {t("common.showMore")}
        </Button>
      )}
    </div>
  );
}

// EventTable is the journal as columns: what happened, to whom, the details, who did it
// and when. Same data as EventRow — the card stacks it, this lines it up so a column can
// be read down.
function EventTable({
  events,
  showUser,
}: {
  events: UserEvent[];
  showUser?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <TableShell bare>
      <THead
        cols={[
          { label: t("events.colAction") },
          ...(showUser ? [{ label: t("events.colUser") }] : []),
          { label: t("events.colDetails"), className: "hidden md:table-cell" },
          { label: t("events.colActor"), className: "hidden sm:table-cell" },
          { label: t("events.colWhen") },
        ]}
      />
      <tbody>
        {events.map((e) => {
          const meta = actionMeta(e.action);
          const details = eventDetails(e);
          return (
            <TR key={e.id}>
              <TD>
                <div className="flex items-center gap-1.5 whitespace-nowrap">
                  <Badge color={meta.color} size="xs">
                    {meta.label}
                  </Badge>
                  {e.details?.bulk === true && (
                    <Badge color="gray" size="xs">
                      {t("events.bulk")}
                    </Badge>
                  )}
                </div>
              </TD>
              {showUser && (
                <TD className="font-medium text-ink">
                  <div className="max-w-[12rem] truncate">{e.user_name || `#${e.user_id}`}</div>
                </TD>
              )}
              <TD className="hidden md:table-cell">
                {details ? (
                  <div className="max-w-[22rem] truncate" title={details}>
                    {details}
                  </div>
                ) : (
                  <span className="text-ink-muted">—</span>
                )}
              </TD>
              <TD className="hidden whitespace-nowrap text-ink-muted sm:table-cell">
                {actorLabel(e)}
              </TD>
              <TD className="whitespace-nowrap text-ink-muted">
                {fmtDateTime(e.created_at)}
              </TD>
            </TR>
          );
        })}
      </tbody>
    </TableShell>
  );
}
