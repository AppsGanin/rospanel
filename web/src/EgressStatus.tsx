import { useEffect, useState } from "react";
import { getRouting, type RoutingInfo } from "./api";
import { Badge, Card } from "./ui";

// Opera region codes → human labels.
const OPERA_REGION: Record<string, string> = {
  EU: "Европа",
  AS: "Азия",
  AM: "Америка",
};

export type LaneStatus = { label: string; color: "green" | "orange" | "gray"; note?: string };

// helperStatus maps a helper-backed lane (Opera) to its status badge:
// off → выключен; not running → запускается…; running+alive → активен;
// running but failing its probe → на фолбэке (Xray routes it to direct).
export function helperStatus(
  enabled: boolean,
  running: boolean,
  alive: boolean,
  note: string,
): LaneStatus {
  if (!enabled) return { label: "выключен", color: "gray" };
  if (!running) return { label: "запускается…", color: "orange", note };
  if (alive) return { label: "активен", color: "green", note };
  return { label: "на фолбэке (direct)", color: "orange", note };
}

// Row renders one egress lane with its status badge.
function Row({ name, status }: { name: string; status: LaneStatus }) {
  return (
    <div className="flex items-center justify-between gap-3 py-2">
      <span className="text-sm font-medium text-ink">{name}</span>
      <div className="flex items-center gap-2">
        {status.note && <span className="text-xs text-ink-muted">{status.note}</span>}
        <Badge color={status.color}>{status.label}</Badge>
      </div>
    </div>
  );
}

export function EgressStatus() {
  const [r, setR] = useState<RoutingInfo | null>(null);

  useEffect(() => {
    const load = () => getRouting().then(setR).catch(() => {});
    load();
    const id = setInterval(load, 15000); // statuses (helper up/down) can change
    return () => clearInterval(id);
  }, []);

  if (!r) return null;

  const warp: LaneStatus = !r.warp_enabled
    ? { label: "выключен", color: "gray" }
    : r.warp_registered
      ? { label: "включён", color: "green" }
      : { label: "не зарегистрирован", color: "orange" };

  const opera = helperStatus(
    r.opera_enabled,
    r.opera_running,
    r.opera_alive,
    OPERA_REGION[r.opera_country] ?? r.opera_country,
  );

  // One row per proxy lane. The count is how many proxies the lane RESOLVED, not
  // how many answer — liveness is Xray's Observatory's call, and the panel doesn't
  // query it (a dead proxy just sends the lane's balancer to direct).
  const lanes = r.config?.lanes ?? [];
  const laneStatus = (id: string, enabled: boolean): LaneStatus => {
    if (!enabled) return { label: "выключена", color: "gray" };
    const n = r.proxy_counts?.[id] ?? 0;
    return n > 0
      ? { label: `${n} прокси`, color: "green" }
      : { label: "нет прокси", color: "orange" };
  };

  return (
    <Card className="p-4">
      <h3 className="mb-1 font-bold text-ink">Роутинг</h3>
      <div className="divide-y divide-gray-100">
        <Row name="Cloudflare WARP" status={warp} />
        <Row name="Opera VPN" status={opera} />
        {lanes.map((l) => (
          <Row
            key={l.id}
            name={l.name?.trim() || l.id}
            status={laneStatus(l.id, l.enabled)}
          />
        ))}
      </div>
    </Card>
  );
}
