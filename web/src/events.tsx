import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { EventPage, UserEvent } from "./api";
import { fmtBytes, fmtSpeed } from "./format";
import i18n, { currentLang, slugKey, td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  EmptyState,
  MICRO,
  Mono,
  Skeletons,
  useWideBox,
} from "./ui";

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
  "user.device_bound": "gray",
  "user.device_refused": "orange",
  "user.device_unbound": "gray",
  "user.policy_refused": "orange",
  "user.abuse_warned": "orange",
  "user.abuse_throttled": "orange",
  "user.abuse_disabled": "red",
  "user.abuse_lifted": "green",
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
  // The label comes from the dictionary whenever it has one, whatever the colour
  // table says: a new action forgotten here must still read as words, in grey.
  const key = `events.action.${slugKey(action)}`;
  const label = i18n.exists(key) ? td(key) : action;
  return { label, color: ACTION_COLORS[action] ?? "gray" };
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
    case "user.device_bound":
    case "user.device_refused": {
      // What the device called itself, and where that put the count against the cap.
      const what = [str(d, "model"), str(d, "os")].filter(Boolean).join(" · ");
      const count = has(d, "device_limit") && num(d, "device_limit") > 0
        ? i18n.t("events.det.devicesOverLimit", { active: num(d, "devices"), limit: num(d, "device_limit") })
        : i18n.t("events.det.devices", { count: num(d, "devices") });
      return [what, count].filter(Boolean).join(" · ");
    }
    case "user.device_unbound":
      return has(d, "devices")
        ? i18n.t("events.det.devices", { count: num(d, "devices") })
        : str(d, "hwid");
    case "user.policy_refused": {
      const where = [str(d, "country"), str(d, "org")].filter(Boolean).join(" · ");
      return i18n.t(d.blocked === true ? "events.det.policyBlocked" : "events.det.policyNoted", { where });
    }
    case "user.abuse_warned":
      return i18n.t("events.det.matches", { count: num(d, "matches") });
    case "user.abuse_throttled":
      return i18n.t("events.det.throttledTo", {
        speed: fmtSpeed(num(d, "speed_limit")),
        hours: num(d, "hours"),
        matches: num(d, "matches"),
      });
    case "user.abuse_disabled":
      return i18n.t("events.det.disabledFor", { hours: num(d, "hours"), matches: num(d, "matches") });
    case "user.abuse_lifted":
      return i18n.t(str(d, "why") === "overruled" ? "events.det.liftedByHand" : "events.det.liftedExpired");
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
  // table renders the trail as columns instead of stacked cards — every row carries
  // the same facts, and scanning down a column is the point. The grid stacks itself
  // below 560px, so it fits a dialog and a phone as well as the journal page.
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

  // A list of rows loads as a list of rows: the trail's shape is the placeholder,
  // not a spinner in the middle of an empty box.
  if (loading)
    return table ? (
      <div className="flex flex-col gap-2.5 border-t border-brand-600/10 p-3.5">
        <Skeletons n={6} className="h-4 w-full" />
      </div>
    ) : (
      <CenterLoader />
    );
  if (!events.length) return <EmptyState title={empty ?? t("events.empty")} />;

  return (
    <div className={cn("flex flex-col", !table && "gap-3")}>
      {table ? (
        <EventGrid events={events} showUser={showUser} />
      ) : (
        <ul className="flex flex-col gap-2">
          {events.map((e) => (
            <EventRow key={e.id} event={e} showUser={showUser} />
          ))}
        </ul>
      )}
      {next > 0 && (
        <div className={cn(table && "border-t border-gray-100 p-3.5")}>
          <Button variant="light" fullWidth loading={more} onClick={loadMore}>
            {t("common.showMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

// The journal's columns: what happened, to whom, the details, who did it and when.
// One template for the header and every row (an inline style — Tailwind must not
// generate a track list per screen), and a two-column stack on a phone, where five
// columns of 12px text would each be a word wide.
const TPL =
  "minmax(0,1.4fr) minmax(0,1fr) minmax(0,2fr) minmax(0,.9fr) minmax(0,.9fr)";
const TPL_NOUSER = "minmax(0,1.4fr) minmax(0,2fr) minmax(0,.9fr) minmax(0,.9fr)";
const TPL_MOBILE = "minmax(0,1fr) auto";
// Below this the five columns are each a word wide; the row stacks instead.
const GRID_WIDE_MIN = 560;

// actorClass: the system is grey because nobody did it — a person is the accent,
// because "who" is the question a journal row is read for.
const actorClass = (kind: string) =>
  kind === "system" ? "text-ink-muted" : "text-accent";

// EventGrid is the journal as columns. Same data as EventRow — the card stacks it,
// this lines it up so a column can be read down.
function EventGrid({
  events,
  showUser,
}: {
  events: UserEvent[];
  showUser?: boolean;
}) {
  const { t } = useTranslation();
  // Measured on the grid itself: inside the sidebar layout a 700px window leaves
  // this box ~430px, and five columns of prose do not fit in that.
  const [boxRef, wide] = useWideBox(GRID_WIDE_MIN);
  const tpl = !wide ? TPL_MOBILE : showUser ? TPL : TPL_NOUSER;
  return (
    <div ref={boxRef} className="border-t border-brand-600/10">
      {wide && (
        <div
          className={cn(MICRO, "grid items-center gap-3 px-3.5 py-2")}
          style={{ gridTemplateColumns: tpl }}
        >
          <span className="truncate">{t("events.colAction")}</span>
          {showUser && <span className="truncate">{t("events.colUser")}</span>}
          <span className="truncate">{t("events.colDetails")}</span>
          <span className="truncate">{t("events.colActor")}</span>
          <span className="truncate text-right">{t("events.colWhen")}</span>
        </div>
      )}
      {events.map((e) => {
        const meta = actionMeta(e.action);
        const details = eventDetails(e);
        const bulk = e.details?.bulk === true;
        const who = actorLabel(e);
        const when = fmtDateTime(e.created_at);
        if (!wide)
          return (
            <div
              key={e.id}
              className="grid items-baseline gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]"
              style={{ gridTemplateColumns: tpl }}
            >
              <span className="truncate text-xs text-ink">
                {meta.label}
                {bulk ? ` · ${t("events.bulk")}` : ""}
              </span>
              <Mono className="text-right text-[11px] text-ink-muted">{when}</Mono>
              <span className="col-span-2 truncate text-xs text-ink-muted">
                {showUser ? `${e.user_name || `#${e.user_id}`} · ` : ""}
                <span className={actorClass(e.actor_kind)}>{who}</span>
                {details ? ` · ${details}` : ""}
              </span>
            </div>
          );
        return (
          <div
            key={e.id}
            className="grid items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
            style={{ gridTemplateColumns: tpl }}
          >
            <span className="truncate text-xs text-ink" title={meta.label}>
              {meta.label}
              {bulk && (
                <span className="ml-1.5 text-[11px] text-ink-muted">
                  {t("events.bulk")}
                </span>
              )}
            </span>
            {showUser && (
              <span className="truncate text-xs text-ink">
                {e.user_name || `#${e.user_id}`}
              </span>
            )}
            <span
              className="truncate text-xs text-ink-muted"
              title={details || undefined}
            >
              {details || "—"}
            </span>
            <span
              className={cn("truncate text-xs", actorClass(e.actor_kind))}
              title={who}
            >
              {who}
            </span>
            <Mono className="text-right text-[11px] text-ink-muted">{when}</Mono>
          </div>
        );
      })}
    </div>
  );
}
