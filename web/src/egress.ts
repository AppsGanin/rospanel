// Shared status vocabulary for egress lanes. The dashboard used to render a
// "Роутинг" card from these; routing is per-server now, so what is left is the
// badge mapping the server cards use.
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
