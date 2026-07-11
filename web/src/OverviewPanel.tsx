import { useEffect, useState } from "react";
import { restartXray, type SystemStatus } from "./api";
import { cssVar } from "./charts";
import { fmtBytes, fmtDuration, plural } from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Badge, Button, Card, Skeleton, useConfirm } from "./ui";
import { XrayLogs } from "./XrayLogs";
import { XrayConfigView } from "./XrayConfig";
import { ManagementCard } from "./Management";
import { EgressStatus } from "./EgressStatus";

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

function OverviewSkeleton() {
  return (
    <div className="flex flex-col gap-4 animate-fade-in">
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
        {[...Array(6)].map((_, i) => (
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
  const [s, setS] = useState<SystemStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [logsOpen, setLogsOpen] = useState(false);
  const [cfgOpen, setCfgOpen] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const { confirm, confirmNode } = useConfirm();

  // Restarting Xray drops every live VPN connection, so it is confirmed first and
  // never fired implicitly. The SSE stream refreshes the card on its own once the
  // new process is up, so there's nothing to refetch here.
  const doRestart = async () => {
    const ok = await confirm({
      title: "Перезапустить Xray?",
      body: "Все активные VPN-подключения будут разорваны — клиенты переподключатся автоматически через несколько секунд. Конфигурация не изменится.",
      confirmLabel: "Перезапустить",
      danger: true,
    });
    if (!ok) return;
    setRestarting(true);
    try {
      await restartXray();
      notifySuccess("Xray перезапущен");
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRestarting(false);
    }
  };

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

  if (!loaded) return <OverviewSkeleton />;
  if (!s) return null;

  const pct = (used: number, total: number) =>
    total > 0 ? (used / total) * 100 : 0;

  return (
    <div className="flex flex-col gap-4">

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

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <InfoCard title="Xray">
          {/* Status and buttons stack, rather than sharing one line: with three
              buttons the row overflowed the card on a phone. Side-by-side is no
              safer at sm — the grid puts this card in a half-width column there, so
              the row is just as tight. Stacking holds at every width. */}
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Badge color={s.xray_running ? "green" : "red"}>
                {s.xray_running ? "● Запущен" : "○ Остановлен"}
              </Badge>
              {s.xray_version && (
                <span className="text-sm text-ink-muted">v{s.xray_version}</span>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button size="xs" variant="light" color="gray" onClick={() => setCfgOpen(true)}>
                Конфиг
              </Button>
              <Button size="xs" variant="light" color="gray" onClick={() => setLogsOpen(true)}>
                Логи
              </Button>
              <Button
                size="xs"
                variant="light"
                color="red"
                loading={restarting}
                onClick={doRestart}
              >
                Рестарт
              </Button>
            </div>
          </div>
        </InfoCard>

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

        <InfoCard title="Общий объём трафика">
          <div className="grid grid-cols-2 gap-4">
            <Metric label="↑ Отправлено" value={fmtBytes(s.total_up)} />
            <Metric label="↓ Получено" value={fmtBytes(s.total_down)} />
          </div>
        </InfoCard>

        <InfoCard title="Сеть сервера">
          <div className="grid grid-cols-2 gap-4">
            <Metric label="↑ Отдача" value={`${fmtBytes(s.net_up)}/s`} />
            <Metric label="↓ Приём" value={`${fmtBytes(s.net_down)}/s`} />
          </div>
        </InfoCard>

        <InfoCard title="VPN-трафик">
          <div className="grid grid-cols-2 gap-4">
            <Metric label="↑ Отдача" value={`${fmtBytes(s.vpn_up)}/s`} />
            <Metric label="↓ Приём" value={`${fmtBytes(s.vpn_down)}/s`} />
          </div>
        </InfoCard>
      </div>

      <EgressStatus />

      <ManagementCard />

      {logsOpen && <XrayLogs onClose={() => setLogsOpen(false)} />}
      {cfgOpen && <XrayConfigView onClose={() => setCfgOpen(false)} />}
      {confirmNode}
    </div>
  );
}
