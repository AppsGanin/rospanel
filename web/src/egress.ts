// Shared status vocabulary for egress lanes. The dashboard used to render a
// "Routing" card from these; routing is per-server now, so what is left is the
// badge mapping the server cards use.
import i18n from "./i18n";

export type LaneStatus = { label: string; color: "green" | "orange" | "gray"; note?: string };

// helperStatus maps a helper-backed lane (Opera) to its status badge:
// off → off; not running → starting…; running+alive → active;
// running but failing its probe → on fallback (Xray routes it to direct).
export function helperStatus(
  enabled: boolean,
  running: boolean,
  alive: boolean,
  note: string,
): LaneStatus {
  if (!enabled) return { label: i18n.t("egress.off"), color: "gray" };
  if (!running) return { label: i18n.t("egress.starting"), color: "orange", note };
  if (alive) return { label: i18n.t("egress.alive"), color: "green", note };
  return { label: i18n.t("egress.fallback"), color: "orange", note };
}
