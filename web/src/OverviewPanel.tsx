import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  getRecentAbuse,
  getStatsSeries,
  listEvents,
  listNodes,
  listUsers,
  restartNodeXray,
  type DailyPoint,
  type NodeView,
  type SystemStatus,
  type User,
  type UserEvent,
} from "./api";
import { type Bar, DayBars } from "./charts";
import { actionMeta, eventDetails } from "./events";
import { fmtBytes, fmtDuration, localDay } from "./format";
import { useAction } from "./hooks";
import { nodeState, serverName, servingCount } from "./NodesPanel";
import { openStream } from "./livestream";
import { useIsAdmin } from "./role";
import { navigate } from "./router";
import {
  Button,
  cn,
  KpiTile,
  MICRO,
  MiniBar,
  Mono,
  Panel,
  Skeleton,
} from "./ui";
import { ManagementCard } from "./Management";

// EXPIRY_SOON is the window the dashboard counts subscriptions as running out in.
// A week is what an operator can still act on: renew, message the customer, or let
// it lapse deliberately.
const EXPIRY_SOON_DAYS = 7;

// SPARK_DAYS is how far back the traffic chart reaches. The panel keeps traffic per
// DAY (model.TrafficDailyRetentionDays), not per hour, so this is a week of days
// rather than a 24-hour curve — and at seven columns every day still gets its date
// and its figure, which is what makes the chart readable at a glance.
const SPARK_DAYS = 7;

// ABUSE_DAYS mirrors model.AbuseRetentionDays: the store keeps a blocklist match for
// two weeks, so a longer window here would count a period the panel cannot see.
const ABUSE_DAYS = 14;

// SLOW_POLL is the cadence for the figures that move by the day — expiring
// subscriptions, the traffic history, blocklist matches. The live numbers come off
// the SSE stream; re-reading a whole user list every two seconds to find out that
// nothing expires today would be a query storm for a figure that cannot change.
const SLOW_POLL = 5 * 60_000;
const NODE_POLL = 30_000;

/* ------------------------------------------------------------------ pieces */

// StatusDot is the state marker that leads a row: colour carries the state, the
// word beside it says which one — never colour alone.
function StatusDot({ className, size = 1.5 }: { className: string; size?: number }) {
  return (
    <span
      className={cn("shrink-0 rounded-full", className)}
      style={{ width: `${size * 4}px`, height: `${size * 4}px` }}
      aria-hidden
    />
  );
}

// AttentionRow is one thing that is wrong and the one control that addresses it.
// The button repeats the action, never "OK": the operator should be able to read
// the row and know what the click will do.
function AttentionRow({
  text,
  when,
  action,
  dot,
  onAction,
  busy,
}: {
  text: string;
  when?: string;
  action: string;
  dot: string;
  onAction: () => void;
  busy?: boolean;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-gray-100 px-3.5 py-2.5 last:border-0">
      <StatusDot className={dot} size={2} />
      <span className="min-w-0 flex-1 truncate text-[13px] text-gray-900">{text}</span>
      {when && <Mono className="shrink-0 text-[11px] text-ink-muted">{when}</Mono>}
      <Button size="xs" variant="outline" loading={busy} onClick={onAction}>
        {action}
      </Button>
    </div>
  );
}

// SERVER_COLS is the servers table, header and rows alike. On a phone the six
// columns become two and the row reads as a card — the design's one rule for dense
// tables, and the reason there is no second component for narrow screens.
const SERVER_COLS =
  "grid-cols-[1fr_auto] sm:grid-cols-[1.5fr_.9fr_1fr_1fr_1fr_.8fr]";

// ServerRow is one server as the dashboard reports it: what it is, whether it is
// serving, how loaded the machine is, and what it carried today. A server that has
// never reported says so across the three load columns rather than showing empty
// bars, which read as an idle machine.
function ServerRow({ node }: { node: NodeView }) {
  const { t } = useTranslation();
  const state = nodeState(node);
  const pct = (used: number, total: number) => (total > 0 ? (used / total) * 100 : 0);
  const traffic = (node.traffic_up ?? 0) + (node.traffic_down ?? 0);
  return (
    <div
      className={cn(
        "grid items-center gap-2.5 border-b border-gray-100 px-3.5 py-2 last:border-0",
        SERVER_COLS,
        // The one row an operator must not scroll past. Far fainter than a chip:
        // it sits under text that still has to read as text.
        state.tone === "warning" && "warning-tint-weak",
      )}
    >
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-xs font-medium text-ink">{serverName(node)}</span>
        {node.host && (
          <Mono className="truncate text-[11px] text-ink-muted">{node.host}</Mono>
        )}
      </span>
      <span
        className={cn(
          "inline-flex items-center gap-1.5 text-xs max-sm:col-start-1",
          state.tone === "success" && "text-success",
          state.tone === "warning" && "text-warning",
          state.tone === "danger" && "text-danger",
          state.tone === "default" && "text-ink-muted",
        )}
      >
        <StatusDot className={state.dot} />
        <span className="truncate">{state.label}</span>
      </span>
      {node.has_host_stats ? (
        <>
          {/* No labels here: the column headings above already name them. */}
          <MiniBar percent={node.cpu_percent} className="max-sm:hidden" />
          <MiniBar percent={pct(node.mem_used, node.mem_total)} className="max-sm:hidden" />
          <MiniBar percent={pct(node.disk_used, node.disk_total)} className="max-sm:hidden" />
        </>
      ) : (
        <span className="text-xs text-ink-muted max-sm:hidden sm:col-span-3">
          {t("overview.noStats")}
        </span>
      )}
      <Mono className="text-right text-xs text-gray-800 max-sm:col-start-2 max-sm:row-start-1">
        {fmtBytes(traffic)}
      </Mono>
    </div>
  );
}

/* ------------------------------------------------------------------ loading */

// The skeleton repeats the layout it is standing in for, so nothing jumps when the
// data lands — a centred spinner would collapse the page and then rebuild it.
function OverviewSkeleton() {
  return (
    <div className="flex animate-fade-in flex-col gap-3.5">
      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-6">
        {[...Array(6)].map((_, i) => (
          <div key={i} className="rounded-xl border border-gray-200 bg-white px-3.5 py-3">
            <Skeleton className="h-2.5 w-16" />
            <Skeleton className="mt-2 h-7 w-14" />
            <Skeleton className="mt-1.5 h-2.5 w-20" />
          </div>
        ))}
      </div>
      <div className="grid gap-3.5 lg:grid-cols-[minmax(0,1.55fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-3.5">
          <Skeleton className="h-40 rounded-xl" />
          <Skeleton className="h-56 rounded-xl" />
        </div>
        <div className="flex flex-col gap-3.5">
          <Skeleton className="h-44 rounded-xl" />
          <Skeleton className="h-44 rounded-xl" />
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------- screen */

export function OverviewPanel() {
  const { t } = useTranslation();
  const isAdmin = useIsAdmin();
  const { isBusy, run } = useAction();
  const [s, setS] = useState<SystemStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [live, setLive] = useState(true);
  const [nodes, setNodes] = useState<NodeView[]>([]);
  const [events, setEvents] = useState<UserEvent[] | null>(null);
  const [users, setUsers] = useState<User[] | null>(null);
  const [series, setSeries] = useState<DailyPoint[] | null>(null);
  const [hoverDay, setHoverDay] = useState<Bar | null>(null);
  const [abuse, setAbuse] = useState<number | null>(null);

  useEffect(() => {
    // Live push via Server-Sent Events, through openStream rather than a bare
    // EventSource: the panel refuses a stream with 429 once the per-IP gate is full,
    // and a bare EventSource treats that as fatal and never comes back — the dashboard
    // would sit frozen on stale numbers with nothing to say so.
    const stream = openStream(
      "api/system/stream",
      (data) => {
        try {
          setS(JSON.parse(data));
          setLoaded(true);
        } catch {
          /* ignore malformed frame */
        }
      },
      setLive,
    );
    return () => stream.close();
  }, []);

  // Nodes and the event tail move on the minute, not on the second, so they poll on
  // their own slow timer instead of riding the 2s status stream. The node list is an
  // admin-only route: an operator never asks for it, and so never sees a panel whose
  // every refresh would answer 403.
  const loadNodes = useCallback(() => {
    if (!isAdmin) return;
    listNodes()
      .then((r) => setNodes(r.nodes))
      .catch(() => {});
  }, [isAdmin]);

  useEffect(() => {
    const load = () => {
      loadNodes();
      listEvents({ limit: 6 })
        .then((p) => setEvents(p.events ?? []))
        .catch(() => setEvents([]));
    };
    load();
    const id = setInterval(load, NODE_POLL);
    return () => clearInterval(id);
  }, [loadNodes]);

  // The day-scale figures. Each lands on its own: a tile whose fetch is still in
  // flight shows a dash rather than holding the whole dashboard back.
  useEffect(() => {
    const load = () => {
      listUsers()
        .then(setUsers)
        .catch(() => setUsers([]));
      getStatsSeries({ from: localDay(SPARK_DAYS - 1), to: localDay(0) })
        .then(setSeries)
        .catch(() => setSeries([]));
      getRecentAbuse(200)
        .then((rows) => {
          const cutoff = Date.now() / 1000 - ABUSE_DAYS * 86400;
          setAbuse(rows.filter((r) => r.last_seen >= cutoff).length);
        })
        .catch(() => setAbuse(0));
    };
    load();
    const id = setInterval(load, SLOW_POLL);
    return () => clearInterval(id);
  }, []);

  if (!loaded) return <OverviewSkeleton />;
  if (!s) return null;

  const pct = (used: number, total: number) => (total > 0 ? (used / total) * 100 : 0);
  const dash = "—";

  /* --- derived figures ------------------------------------------------- */

  // Only remote nodes count as a fleet: on a single-server install the host panel
  // below already describes the only machine there is, so neither the tile nor the
  // servers panel is drawn at all.
  const remote = nodes.filter((n) => !n.is_local);
  // A fleet is what makes the nodes tile and the "N of M" note worth printing; the
  // servers table itself is drawn for every admin, because with the host panel gone
  // it is the only place the machines' load is reported.
  const hasFleet = isAdmin && remote.length > 0;
  const showServers = isAdmin;
  const serving = servingCount(nodes);
  const fleetNote = [
    remote.filter((n) => n.enabled && n.joined && n.online && !n.xray_running).length &&
      t("overview.nNoXray", {
        count: remote.filter((n) => n.enabled && n.joined && n.online && !n.xray_running)
          .length,
      }),
    remote.filter((n) => n.enabled && n.joined && !n.online).length &&
      t("overview.nOffline", {
        count: remote.filter((n) => n.enabled && n.joined && !n.online).length,
      }),
    remote.filter((n) => n.enabled && !n.joined).length &&
      t("overview.nNotJoined", {
        count: remote.filter((n) => n.enabled && !n.joined).length,
      }),
    remote.filter((n) => !n.enabled).length &&
      t("overview.nDisabled", { count: remote.filter((n) => !n.enabled).length }),
  ]
    .filter(Boolean)
    .join(", ");

  const now = Date.now() / 1000;
  const expiring =
    users === null
      ? null
      : users.filter(
          (u) =>
            u.expire_at > 0 &&
            u.expire_at > now &&
            u.expire_at - now <= EXPIRY_SOON_DAYS * 86400,
        ).length;

  // "vs yesterday" needs both days to be real: yesterday at zero makes any change
  // infinite, and a panel installed today has no yesterday to compare with.
  const today = localDay(0);
  const yesterday = localDay(1);
  const dayTotal = (day: string) => {
    const p = series?.find((x) => x.day === day);
    return p ? p.up + p.down : 0;
  };
  const prev = dayTotal(yesterday);
  const current = dayTotal(today);
  // Both days have to be real. Yesterday at zero makes any change infinite; today at
  // zero is every panel at ten past midnight, and "−100% ко вчера" then reports the
  // clock rather than the traffic.
  const trend =
    series && prev > 0 && current > 0
      ? Math.round(((current - prev) / prev) * 100)
      : null;
  const sparkTotal = (series ?? []).reduce((a, p) => a + p.up + p.down, 0);
  const sparkDays = (series ?? []).map((p) => ({
    day: p.day,
    value: p.up + p.down,
    today: p.day === today,
  }));

  /* --- what needs doing ------------------------------------------------ */

  type Attention = {
    key: string;
    dot: string;
    text: string;
    when?: string;
    action: string;
    onAction: () => void;
    busy?: boolean;
  };

  const attention: Attention[] = [];
  for (const n of remote) {
    if (!n.enabled) continue; // switched off on purpose is not a fault
    const since = n.last_seen ? fmtDuration(now - n.last_seen) : undefined;
    if (!n.joined) {
      attention.push({
        key: `join-${n.id}`,
        dot: "bg-gray-400",
        text: t("overview.attnNotJoined", { name: serverName(n) }),
        action: t("overview.open"),
        onAction: () => navigate("nodes"),
      });
    } else if (!n.online) {
      attention.push({
        key: `off-${n.id}`,
        dot: "bg-danger",
        text: t("overview.attnOffline", { name: serverName(n) }),
        when: since,
        action: t("overview.open"),
        onAction: () => navigate("nodes"),
      });
    } else if (!n.xray_running) {
      attention.push({
        key: `xray-${n.id}`,
        dot: "bg-warning",
        text: t("overview.attnXrayDown", { name: serverName(n) }),
        when: since,
        action: t("overview.start"),
        busy: isBusy(`xray-${n.id}`),
        // The same request the server card sends: the panel can only ask, and the
        // node's next check-in is what proves it happened — so refresh the list
        // after, rather than claiming success here.
        onAction: () =>
          run(
            async () => {
              await restartNodeXray(n.id);
              loadNodes();
            },
            { key: `xray-${n.id}` },
          ),
      });
    }
  }
  if (expiring) {
    attention.push({
      key: "expiring",
      dot: "bg-brand-600",
      text: t("overview.attnExpiring", { count: expiring }),
      action: t("overview.open"),
      onAction: () => navigate("users"),
    });
  }

  // Nothing wrong, nothing to say: an empty "needs attention" panel is a claim in
  // itself, and a false one the moment it goes stale.
  const attentionPanel = attention.length > 0 && (
    <Panel
      title={t("overview.attention")}
      aside={<Mono className="text-[11px] text-ink-muted">{attention.length}</Mono>}
    >
      {attention.map((a) => (
        <AttentionRow
          key={a.key}
          dot={a.dot}
          text={a.text}
          when={a.when}
          action={a.action}
          busy={a.busy}
          onAction={a.onAction}
        />
      ))}
    </Panel>
  );

  /* --- render ---------------------------------------------------------- */

  return (
    <div className="flex flex-col gap-3.5">
      {/* Stale numbers must say so. Without this the dashboard is indistinguishable
          from a quiet server: the same figures, forever. */}
      {!live && (
        <div className="warning-tint rounded-lg px-3 py-2 text-sm text-warning">
          {t("overview.reconnecting")}
        </div>
      )}

      {/* The figures the panel is opened to read, before anyone drills into a page. */}
      {/* Explicit counts, not auto-fit: auto-fit picks the column count from the
          width and lands on five-plus-one at ordinary desktop sizes. The row is
          read as a row. */}
      <div
        className={cn(
          "grid gap-2.5 grid-cols-2 sm:grid-cols-3",
          hasFleet ? "lg:grid-cols-6" : "lg:grid-cols-5",
        )}
      >
        <KpiTile
          label={t("nav.users")}
          value={s.users}
          note={t("overview.nActive", { count: s.enabled_users })}
        />
        <KpiTile
          label={t("overview.online")}
          value={s.online_users}
          tone={s.online_users > 0 ? "success" : "default"}
        />
        <KpiTile
          label={t("overview.trafficToday")}
          value={fmtBytes(s.traffic_today)}
          note={
            trend === null
              ? undefined
              : t("overview.vsYesterday", {
                  pct: `${trend > 0 ? "+" : ""}${trend}%`,
                })
          }
        />
        {hasFleet && (
          <KpiTile
            label={t("health.nodes")}
            value={`${serving}/${nodes.length}`}
            note={fleetNote}
            tone={serving < nodes.length ? "warning" : "success"}
          />
        )}
        <KpiTile
          label={t("overview.expiring")}
          value={expiring === null ? dash : expiring}
          note={t("overview.expiringNote")}
        />
        <KpiTile
          label={t("overview.blocklists")}
          value={abuse === null ? dash : abuse}
          note={t("overview.blocklistNote", { n: ABUSE_DAYS })}
          tone={abuse ? "warning" : "default"}
        />
      </div>

      {/* Without a fleet there is no left column to fill, so the attention panel takes
          the width and the three read-outs sit side by side instead of stretching one
          sparkline across the screen. Same panels, placed by how many there are. */}
      {!showServers && attentionPanel}

      <div
        className={cn(
          "grid gap-3.5",
          showServers
            ? "lg:grid-cols-[minmax(0,1.55fr)_minmax(0,1fr)]"
            : "md:grid-cols-2 xl:grid-cols-3",
        )}
      >
        {showServers && (
          <div className="flex min-w-0 flex-col gap-3.5">
            {attentionPanel}

            <Panel
              title={t("nav.servers")}
              aside={
                nodes.length > 1 && (
                  <span className="shrink-0 text-xs text-ink-muted">
                    {t("overview.serversNote", { up: serving, total: nodes.length })}
                  </span>
                )
              }
            >
              <div
                className={cn(
                  MICRO,
                  "grid gap-2.5 border-b border-gray-200 px-3.5 py-2 max-sm:hidden",
                  SERVER_COLS,
                )}
              >
                <span>{t("nodes.server")}</span>
                <span>{t("usersPanel.colStatus")}</span>
                <span>CPU</span>
                <span>RAM</span>
                <span>{t("overview.disk")}</span>
                <span className="text-right">{t("usersPanel.colTraffic")}</span>
              </div>
              {nodes.length === 0
                ? [...Array(2)].map((_, i) => (
                    <div key={i} className="border-b border-gray-100 px-3.5 py-3 last:border-0">
                      <Skeleton className="h-3.5 w-full" />
                    </div>
                  ))
                : nodes.map((n) => <ServerRow key={n.id} node={n} />)}
            </Panel>
          </div>
        )}

        {/* `contents` dissolves this wrapper when there is no left column, so the
            three panels become grid items of the row above rather than a stack. */}
        <div className={showServers ? "flex min-w-0 flex-col gap-3.5" : "contents"}>
          <Panel
            pad
            title={t("overview.trafficDays", { n: SPARK_DAYS })}
            aside={
              // The header carries the fortnight's total — or, while the pointer is
              // on a column, that day's figure. Hovering a chart should answer
              // "how much, and when", not just enlarge a bar.
              <Mono className="shrink-0 text-xs text-gray-800">
                {series === null
                  ? dash
                  : hoverDay
                    ? `${hoverDay.label} · ${fmtBytes(hoverDay.value)}`
                    : fmtBytes(sparkTotal)}
              </Mono>
            }
          >
            {series === null ? (
              <Skeleton className="h-24 w-full" />
            ) : sparkTotal === 0 ? (
              // A flat line on the floor reads as a measurement; "no data" is the
              // truth on a panel that has not carried anything yet.
              <p className="py-9 text-center text-xs text-ink-muted">
                {t("stats.noData")}
              </p>
            ) : (
              <DayBars data={sparkDays} fmt={fmtBytes} onHover={setHoverDay} />
            )}
          </Panel>

          <Panel title={t("overview.recentEvents")} className="min-w-0">
            {events === null ? (
              <div className="flex flex-col gap-2 p-3.5">
                {[...Array(4)].map((_, i) => (
                  <Skeleton key={i} className="h-3.5 w-full" />
                ))}
              </div>
            ) : events.length === 0 ? (
              <p className="p-3.5 text-xs text-ink-muted">{t("overview.noEvents")}</p>
            ) : (
              events.map((e) => <EventLine key={e.id} event={e} />)
            )}
          </Panel>

          {/* Backup, restore, restart and the factory reset. Admin-only on the
              server, so an operator is not shown controls whose every call would
              403. */}
          {isAdmin && <ManagementCard />}
        </div>
      </div>
    </div>
  );
}

// EventLine is one line of the journal tail: when, then who and what. Details are
// part of the sentence ("new device, iPhone"), not a second line — the panel next to
// it is a glance, and the full trail is a click away in the journal.
function EventLine({ event }: { event: UserEvent }) {
  const details = eventDetails(event);
  const who = event.user_name || (event.user_id ? `#${event.user_id}` : "");
  const what = [actionMeta(event.action).label, details].filter(Boolean).join(", ");
  const time = new Date(event.created_at * 1000).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
  return (
    <div className="flex gap-2.5 border-b border-gray-100 px-3.5 py-2 last:border-0">
      <Mono className="shrink-0 text-[11px] leading-4 text-ink-muted">{time}</Mono>
      <span className="min-w-0 flex-1 truncate text-xs leading-4 text-gray-800">
        {who ? `${who} — ${what}` : what}
      </span>
    </div>
  );
}
