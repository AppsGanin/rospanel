import { useEffect, useState } from "react";
import { listNodes, type NodeView, type SystemStatus } from "./api";
import { cssVar } from "./charts";
import { fmtBytes, fmtDuration, plural } from "./format";
import { serverName, statusDot } from "./NodesPanel";
import { useIsAdmin } from "./role";
import { navigate } from "./router";
import { Badge, Card, Skeleton } from "./ui";
import { ManagementCard } from "./Management";

function Gauge({
  percent,
  label,
  value,
}: {
  percent: number;
  label: string;
  value: string;
}) {
  const p = Math.max(0, Math.min(100, percent || 0));
  const r = 40;
  const c = 2 * Math.PI * r;
  const dash = (p / 100) * c;
  const color =
    p < 70 ? cssVar("--color-brand-600", "#0d4cd3") : p < 90 ? "#f97316" : "#ef4444";
  const track = cssVar("--color-gray-200", "#e7eef9");
  return (
    <div className="flex flex-col items-center gap-2">
      <div className="relative h-24 w-24">
        <svg viewBox="0 0 100 100" className="h-full w-full -rotate-90">
          <circle
            cx="50"
            cy="50"
            r={r}
            fill="none"
            stroke={track}
            strokeWidth="9"
          />
          <circle
            cx="50"
            cy="50"
            r={r}
            fill="none"
            stroke={color}
            strokeWidth="9"
            strokeLinecap="round"
            strokeDasharray={`${dash} ${c}`}
            style={{
              transition: "stroke-dasharray 0.5s ease, stroke 0.3s ease",
            }}
          />
        </svg>
        <div className="absolute inset-0 flex items-center justify-center text-sm font-bold text-ink">
          {p.toFixed(p < 10 ? 1 : 0)}%
        </div>
      </div>
      <div className="text-center">
        <p className="text-sm font-bold text-ink">{label}</p>
        <p className="text-xs text-ink-muted">{value}</p>
      </div>
    </div>
  );
}

function InfoCard({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Card className="flex h-full flex-col justify-between p-4">
      <h3 className="mb-3 font-bold text-ink">{title}</h3>
      {children}
    </Card>
  );
}

function Metric({
  label,
  value,
  valueClass,
}: {
  label: string;
  value: string;
  valueClass?: string;
}) {
  return (
    <div>
      <p className="text-xs text-ink-muted">{label}</p>
      <p className={`text-lg font-bold ${valueClass ?? "text-ink"}`}>{value}</p>
    </div>
  );
}

// Kpi is one figure on the top row — the numbers an operator opens the panel to
// read, before drilling into any page.
function Kpi({
  label,
  value,
  valueClass,
}: {
  label: string;
  value: string;
  valueClass?: string;
}) {
  return (
    <div>
      <p className="text-xs text-ink-muted">{label}</p>
      <p className={`text-2xl font-bold ${valueClass ?? "text-ink"}`}>{value}</p>
    </div>
  );
}

// FleetStrip is every server's connectivity in one line, and a shortcut to the page
// that can fix it. It only renders on a multi-server install: with no nodes the
// gauges below already describe the only server there is.
function FleetStrip({ nodes }: { nodes: NodeView[] }) {
  const remote = nodes.filter((n) => !n.is_local);
  if (remote.length === 0) return null;
  // The badge names the worst thing about the fleet, and says "все работают" only
  // when it is true of every server — a grey dot for a node that was never installed
  // must not hide behind a green summary. "Serving", not "reachable": a server whose
  // Xray is down counts as broken however promptly its agent answers.
  const offline = remote.filter((n) => n.enabled && n.joined && !n.online).length;
  const dead = nodes.filter(
    (n) => n.enabled && n.joined && (n.is_local || n.online) && !n.xray_running,
  ).length;
  const pending = remote.filter((n) => n.enabled && !n.joined).length;
  const disabled = remote.filter((n) => !n.enabled).length;
  return (
    <Card className="p-4" onClick={() => navigate("nodes")}>
      <div className="mb-3 flex items-center justify-between gap-3">
        <h3 className="font-bold text-ink">Сервера</h3>
        {offline > 0 ? (
          <Badge color="red" size="xs">{offline} офлайн</Badge>
        ) : dead > 0 ? (
          <Badge color="orange" size="xs">{dead} без Xray</Badge>
        ) : pending > 0 ? (
          <Badge color="gray" size="xs">
            {pending} {plural(pending, "не подключена", "не подключены", "не подключены")}
          </Badge>
        ) : disabled > 0 ? (
          <Badge color="gray" size="xs">
            {disabled} {plural(disabled, "выключена", "выключены", "выключены")}
          </Badge>
        ) : (
          <Badge color="green" size="xs">все работают</Badge>
        )}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {nodes.map((n) => (
          <span
            key={n.id}
            className="flex min-w-0 items-center gap-1.5 text-sm text-ink-muted"
          >
            <span className={`h-2 w-2 shrink-0 rounded-full ${statusDot(n)}`} />
            <span className="truncate">{serverName(n)}</span>
          </span>
        ))}
      </div>
    </Card>
  );
}

function OverviewSkeleton() {
  return (
    <div className="flex flex-col gap-4 animate-fade-in">
      <Card className="p-4">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="flex flex-col gap-1.5">
              <Skeleton className="h-3 w-16" />
              <Skeleton className="h-8 w-20" />
            </div>
          ))}
        </div>
      </Card>
      <Card className="p-4">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="flex flex-col items-center gap-2">
              <Skeleton className="h-24 w-24 rounded-full" />
              <Skeleton className="h-4 w-10" />
              <Skeleton className="h-3 w-24" />
            </div>
          ))}
        </div>
      </Card>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {[...Array(4)].map((_, i) => (
          <Card key={i} className="p-4">
            <Skeleton className="mb-3 h-5 w-20" />
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-1.5">
                <Skeleton className="h-3 w-14" />
                <Skeleton className="h-7 w-20" />
              </div>
              <div className="flex flex-col gap-1.5">
                <Skeleton className="h-3 w-14" />
                <Skeleton className="h-7 w-20" />
              </div>
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}

export function OverviewPanel() {
  const isAdmin = useIsAdmin();
  const [s, setS] = useState<SystemStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [nodes, setNodes] = useState<NodeView[]>([]);
  useEffect(() => {
    // Live push via Server-Sent Events — the server streams updates every 2s and
    // EventSource auto-reconnects if the stream drops.
    const es = new EventSource("api/system/stream", { withCredentials: true });
    es.onmessage = (e) => {
      try {
        setS(JSON.parse(e.data));
        setLoaded(true);
      } catch {
        /* ignore malformed frame */
      }
    };
    return () => es.close();
  }, []);

  // The node list is an admin-only route, so an operator never asks for it (and never
  // sees a strip that would answer 403). It polls on a slow timer of its own rather
  // than riding the 2s status stream: it costs a query per tick and a server's state
  // does not change second to second.
  useEffect(() => {
    if (!isAdmin) return;
    const load = () =>
      listNodes()
        .then((r) => setNodes(r.nodes))
        .catch(() => {});
    load();
    const id = setInterval(load, 30000);
    return () => clearInterval(id);
  }, [isAdmin]);

  if (!loaded) return <OverviewSkeleton />;
  if (!s) return null;

  const pct = (used: number, total: number) =>
    total > 0 ? (used / total) * 100 : 0;

  return (
    <div className="flex flex-col gap-4">

      {/* The numbers the panel exists to report, above the machine it runs on. */}
      <Card className="p-4">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Kpi label="Пользователи" value={String(s.users)} />
          <Kpi label="Активные" value={String(s.enabled_users)} />
          <Kpi
            label="Онлайн"
            value={String(s.online_users)}
            valueClass={s.online_users > 0 ? "text-success" : "text-ink"}
          />
          <Kpi label="Трафик за сегодня" value={fmtBytes(s.traffic_today)} />
        </div>
      </Card>

      {isAdmin && <FleetStrip nodes={nodes} />}

      {/* Resource gauges. */}
      <Card className="p-4">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Gauge
            percent={s.cpu_percent}
            label="CPU"
            value={`${s.cpu_cores} ${plural(s.cpu_cores, 'ядро', 'ядра', 'ядер')}`}
          />
          <Gauge
            percent={pct(s.mem_used, s.mem_total)}
            label="RAM"
            value={`${fmtBytes(s.mem_used)} / ${fmtBytes(s.mem_total)}`}
          />
          <Gauge
            percent={pct(s.swap_used, s.swap_total)}
            label="Swap"
            value={
              s.swap_total > 0
                ? `${fmtBytes(s.swap_used)} / ${fmtBytes(s.swap_total)}`
                : "нет"
            }
          />
          <Gauge
            percent={pct(s.disk_used, s.disk_total)}
            label="Диск"
            value={`${fmtBytes(s.disk_used)} / ${fmtBytes(s.disk_total)}`}
          />
        </div>
      </Card>

      {/* No Xray card here: its status, config, logs and restart all live on one
          server card in «Сервера», next to the same controls for every node. */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <InfoCard title="Время работы">
          <div className="grid grid-cols-2 gap-4">
            <Metric label="Xray" value={fmtDuration(s.xray_uptime)} />
            <Metric label="Система" value={fmtDuration(s.host_uptime)} />
          </div>
        </InfoCard>

        <InfoCard title="Использование">
          <div className="grid grid-cols-2 gap-4">
            <Metric label="RAM панели" value={fmtBytes(s.proc_mem)} />
            <Metric label="Потоки" value={String(s.goroutines)} />
          </div>
        </InfoCard>

        {/* No traffic cards here at all. The live "VPN-трафик" rate was the master's
            own Xray only — nodes report accumulated deltas, not a rate — so on a
            multi-server panel it read as the fleet's throughput while showing one
            server's. Per-server traffic is on each card in «Сервера», and the honest
            fleet total is the per-day history on "Статистика". (An older card summing
            users.used_up/down went for a related reason: the quota reset zeroes it per
            user, so it added up a different period for everybody.) */}
      </div>

      {/* No egress/routing card either — routing is per-server now and reads next to
          the server it belongs to, in «Сервера». Управление holds backup/restore, the
          restart and the factory reset — admin-only on the server. */}
      {isAdmin && <ManagementCard />}

    </div>
  );
}
