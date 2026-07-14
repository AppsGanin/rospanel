import { useMemo, type ReactNode } from "react";
import { type EgressLane, type GeoFile, type RoutingConfig } from "./api";
import { fmtBytes } from "./format";
import {
  Badge,
  Button,
  cn,
  IconChevron,
  Select,
  SegmentedControl,
  Switch,
  TagsInput,
  TextInput,
  ToggleRow,
} from "./ui";

// Section is the flat settings block used across the server settings dialogs: a
// subtly-tinted bordered panel with an optional header (title + description) and a
// right-aligned action slot (a toggle, a badge, a button). Replaces the heavier
// shadowed Card so the blocks read as one calm settings surface inside the modal.
export function Section({
  title,
  desc,
  action,
  children,
  className,
}: {
  title?: ReactNode;
  desc?: string;
  action?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  const hasHeader = !!(title || action);
  return (
    <section
      className={cn(
        "rounded-xl border border-gray-200/80 bg-gray-50/60 p-4",
        className,
      )}
    >
      {hasHeader && (
        <div
          className={cn(
            "flex items-start justify-between gap-3",
            children != null && "mb-4",
          )}
        >
          <div className="min-w-0">
            {title && <p className="font-semibold text-ink">{title}</p>}
            {desc && <p className="mt-0.5 text-sm text-ink-muted">{desc}</p>}
          </div>
          {action && <div className="shrink-0">{action}</div>}
        </div>
      )}
      {children != null && (
        <div className="flex flex-col gap-4">{children}</div>
      )}
    </section>
  );
}

// A small colour union shared by the status badges the parent computes.
export type BadgeColor = "gray" | "green" | "orange" | "red";
export type StatusBadge = { label: string; color: BadgeColor };
export type Opt = { value: string; label: string };

// PROXY_REFRESH are the URL auto-refresh cadence options (minutes; -1 = never).
export const PROXY_REFRESH: Opt[] = [
  { value: "30", label: "Каждые 30 минут" },
  { value: "60", label: "Каждый 1 час" },
  { value: "180", label: "Каждые 3 часа" },
  { value: "360", label: "Каждые 6 часов" },
  { value: "720", label: "Каждые 12 часов" },
  { value: "-1", label: "Никогда" },
];

// EMPTY is a blank routing config with sane defaults (built-in lanes in precedence).
export const EMPTY: RoutingConfig = {
  block_bittorrent: false,
  block_ads: false,
  block_ips: [],
  block_domains: [],
  warp_domains: [],
  warp_ips: [],
  opera_domains: [],
  opera_ips: [],
  direct_domains: [],
  direct_ips: [],
  routing_order: ["warp", "opera", "direct"],
  lanes: [],
  proxy_refresh_minutes: 30,
};

// BUILTIN_LANE_NAMES label the always-present lanes in the routing-order card.
// A proxy lane is labelled by its own name instead.
const BUILTIN_LANE_NAMES: Record<string, string> = {
  direct: "Напрямую",
  warp: "WARP",
  opera: "Opera VPN",
};

// Opera VPN regions opera-proxy supports.
export const OPERA_COUNTRIES = [
  { value: "EU", label: "Европа" },
  { value: "AS", label: "Азия" },
  { value: "AM", label: "Америка" },
];

// BUILTIN_LANES are the lanes that always exist, in default precedence. Mirrors
// model.BuiltinLanes in internal/model/model.go.
const BUILTIN_LANES = ["warp", "opera", "direct"];

// MAX_LANES mirrors model.MaxEgressLanes.
const MAX_LANES = 16;

// fmtWhen renders a unix timestamp as a local date+time, or a dash when unset.
const fmtWhen = (unix: number) =>
  unix
    ? new Date(unix * 1000).toLocaleString("ru-RU", {
        dateStyle: "short",
        timeStyle: "short",
      })
    : "—";

// normalizeOrder returns a routing order containing every existing lane exactly
// once: the config's proxy lanes plus the built-ins. It keeps the saved
// precedence, drops lanes that no longer exist, and inserts missing ones just
// before the catch-all last lane. Mirrors normalizeOrder in xray/generate.go.
export function normalizeOrder(
  order: string[] | undefined,
  laneIDs: string[],
): string[] {
  const known = [...laneIDs, ...BUILTIN_LANES];
  const valid = new Set(known);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const l of order ?? []) {
    if (valid.has(l) && !seen.has(l)) {
      seen.add(l);
      out.push(l);
    }
  }
  const missing = known.filter((l) => !seen.has(l));
  if (missing.length === 0) return out;
  if (out.length === 0) return missing;
  const last = out[out.length - 1];
  return [...out.slice(0, -1), ...missing, last];
}

// newLaneID picks the lowest free "lN" slug. IDs must be lowercase alphanumerics
// with NO dashes — an Xray balancer selects its members by tag prefix, and a dash
// would let one lane's selector swallow another's proxies (see model.ValidLaneID).
function newLaneID(lanes: EgressLane[]): string {
  const taken = new Set(lanes.map((l) => l.id));
  for (let i = 1; ; i++) {
    const id = `l${i}`;
    if (!taken.has(id)) return id;
  }
}

// LaneSource is which proxy source a lane is edited with. Only the selected one
// is persisted (see effectiveCfg), so a lane never silently mixes both.
export type LaneSource = "urls" | "manual";

// laneSources derives each lane's source mode from what it actually carries. A
// lane with URLs is URL-sourced; anything else (incl. a brand-new empty lane) is
// edited as a manual list — the common case for one's own socks5 servers.
export function laneSources(lanes: EgressLane[]): Record<string, LaneSource> {
  const out: Record<string, LaneSource> = {};
  for (const l of lanes) out[l.id] = l.urls.length > 0 ? "urls" : "manual";
  return out;
}

// hydrateRouting normalizes a routing config from the API (Go marshals empty slices
// as null) into one with every list present and a normalized routing order — safe to
// hand straight to the editor. Used by both the master panel and the node dialog.
export function hydrateRouting(
  x: Partial<RoutingConfig> | null | undefined,
): RoutingConfig {
  const src = x ?? {};
  const lanes = (src.lanes ?? []).map((l) => ({
    ...l,
    urls: l.urls ?? [],
    manual: l.manual ?? [],
    domains: l.domains ?? [],
    ips: l.ips ?? [],
  }));
  return {
    block_bittorrent: !!src.block_bittorrent,
    block_ads: !!src.block_ads,
    block_ips: src.block_ips ?? [],
    block_domains: src.block_domains ?? [],
    warp_domains: src.warp_domains ?? [],
    warp_ips: src.warp_ips ?? [],
    opera_domains: src.opera_domains ?? [],
    opera_ips: src.opera_ips ?? [],
    direct_domains: src.direct_domains ?? [],
    direct_ips: src.direct_ips ?? [],
    lanes,
    routing_order: normalizeOrder(
      src.routing_order,
      lanes.map((l) => l.id),
    ),
    // 0 (absent / pre-feature default) shows as 30; -1 stays "never".
    proxy_refresh_minutes: src.proxy_refresh_minutes || 30,
  };
}

// GEO_CADENCE are the geo auto-refresh options (hours; 0 = never).
export const GEO_CADENCE: Opt[] = [
  { value: "0", label: "Никогда (только вручную)" },
  { value: "24", label: "Раз в день" },
  { value: "72", label: "Раз в 3 дня" },
  { value: "168", label: "Раз в неделю" },
];

// GeoSection is the geosite/geoip status + manual refresh + auto-refresh cadence.
// It's the panel's own geo files (used by every server's routing rules, and pushed to
// nodes), so it lives in its own tab on the master card. The cadence applies to the
// master AND every node.
export function GeoSection({
  status,
  onRefresh,
  refreshing,
  cadence,
  onCadence,
}: {
  status: GeoFile[];
  onRefresh: () => void;
  refreshing: boolean;
  cadence: number;
  onCadence: (hours: number) => void;
}) {
  return (
    <Section
      title="Geo-базы"
      desc="geosite.dat / geoip.dat — категории доменов и IP для правил роутинга. Общие для всех серверов (панель раздаёт их нодам)."
      action={
        <Button variant="light" size="sm" loading={refreshing} onClick={onRefresh}>
          Обновить
        </Button>
      }
    >
      <div className="flex flex-col gap-1 text-sm">
        {status.map((f) => (
          <div key={f.name} className="flex items-center justify-between gap-2">
            <span className="font-mono text-ink text-xs">{f.name}</span>
            <span className="text-ink-muted text-xs">
              {f.present
                ? `${fmtBytes(f.size)} · обновлено ${fmtWhen(f.modified_at)}`
                : "нет файла"}
            </span>
          </div>
        ))}
      </div>
      <div>
        <Select
          label="Автообновление"
          data={GEO_CADENCE}
          value={String(cadence)}
          onChange={(v) => onCadence(Number(v))}
        />
        <p className="mt-1 text-xs text-ink-muted">
          Панель сама перекачивает geo по расписанию — и на мастере, и на нодах.
        </p>
      </div>
    </Section>
  );
}

// effectiveCfg drops each lane's non-selected source list, so what's saved and
// compared for "dirty" never carries a stale URL/manual list the operator toggled
// away from. Both the master and node containers call this before saving.
export function effectiveCfg(
  cfg: RoutingConfig,
  laneSrc: Record<string, LaneSource>,
): RoutingConfig {
  return {
    ...cfg,
    lanes: cfg.lanes.map((l) => ({
      ...l,
      urls: laneSrc[l.id] === "urls" ? l.urls : [],
      manual: laneSrc[l.id] === "urls" ? [] : l.manual,
    })),
  };
}

// RoutingEditor is the controlled, container-agnostic routing/egress editor shared
// by the master's routing tab and every node's settings dialog. It owns NO state:
// the parent holds cfg/laneSrc/WARP/Opera and drives saving (the master via a
// SaveBar, a node via its dialog footer). Live lane/helper status is passed in via
// the *Badge props and proxyCounts; a node (whose egress runs remotely) passes
// liveStatus={false} so lane badges don't claim a proxy count the panel can't see.
export function RoutingEditor({
  cfg,
  onCfg,
  laneSrc,
  setLaneSrc,
  warpEnabled,
  setWarpEnabled,
  warpBadge,
  operaEnabled,
  setOperaEnabled,
  operaCountry,
  setOperaCountry,
  operaBadge,
  proxyCounts,
  geosite,
  geoip,
  applying,
  liveStatus = true,
}: {
  cfg: RoutingConfig;
  onCfg: (patch: Partial<RoutingConfig>) => void;
  laneSrc: Record<string, LaneSource>;
  setLaneSrc: React.Dispatch<React.SetStateAction<Record<string, LaneSource>>>;
  warpEnabled: boolean;
  setWarpEnabled: (v: boolean) => void;
  warpBadge: StatusBadge;
  operaEnabled: boolean;
  setOperaEnabled: (v: boolean) => void;
  operaCountry: string;
  setOperaCountry: (v: string) => void;
  operaBadge: StatusBadge;
  proxyCounts: Record<string, number>;
  geosite: string[];
  geoip: string[];
  applying: boolean;
  liveStatus?: boolean;
}) {
  const set = onCfg;

  const moveLane = (i: number, dir: -1 | 1) => {
    const order = [...cfg.routing_order];
    const j = i + dir;
    if (j < 0 || j >= order.length) return;
    [order[i], order[j]] = [order[j], order[i]];
    set({ routing_order: order });
  };

  // laneLabel names a routing-order entry: a built-in lane by its fixed label, a
  // proxy lane by the name the operator gave it.
  const laneLabel = (id: string) =>
    BUILTIN_LANE_NAMES[id] ??
    cfg.lanes.find((l) => l.id === id)?.name?.trim() ??
    id;

  const patchLane = (id: string, patch: Partial<EgressLane>) =>
    set({
      lanes: cfg.lanes.map((l) => (l.id === id ? { ...l, ...patch } : l)),
    });

  // A new lane goes into the order just above the catch-all, so it takes effect
  // (specific rules are only emitted for non-catch-all lanes) without silently
  // stealing the "everything else" slot from whatever holds it.
  const addLane = () => {
    const id = newLaneID(cfg.lanes);
    const lane: EgressLane = {
      id,
      name: `Полоса ${cfg.lanes.length + 1}`,
      enabled: true,
      urls: [],
      manual: [],
      domains: [],
      ips: [],
    };
    const order = [...cfg.routing_order];
    order.splice(Math.max(order.length - 1, 0), 0, id);
    setLaneSrc((s) => ({ ...s, [id]: "manual" }));
    set({ lanes: [...cfg.lanes, lane], routing_order: order });
  };

  const removeLane = (id: string) =>
    set({
      lanes: cfg.lanes.filter((l) => l.id !== id),
      routing_order: cfg.routing_order.filter((l) => l !== id),
    });

  // Preset option lists from the geo databases. geosite categories feed the
  // domain fields, geoip the IP field. A value already chosen in another
  // category is hidden here so the same rule isn't added twice.
  const geositeOpts = useMemo<Opt[]>(
    () => geosite.map((c) => ({ value: `geosite:${c}`, label: c })),
    [geosite],
  );
  const geoipOpts = useMemo<Opt[]>(
    () => geoip.map((c) => ({ value: `geoip:${c}`, label: c })),
    [geoip],
  );
  const without = (opts: Opt[], used: string[]) => {
    const u = new Set(used);
    return opts.filter((o) => !u.has(o.value));
  };

  // laneStatus counts the proxies the lane RESOLVED (manual entries + whatever its
  // URL sources served). It is NOT a liveness signal. On a node the panel can't see
  // the remote count, so we only show enabled/disabled there (liveStatus=false).
  const laneStatus = (lane: EgressLane): StatusBadge => {
    if (!lane.enabled) return { label: "выключена", color: "gray" };
    if (!liveStatus) return { label: "включена", color: "green" };
    const n = proxyCounts[lane.id] ?? 0;
    return n > 0
      ? { label: `${n} прокси`, color: "green" }
      : { label: "нет прокси", color: "orange" };
  };

  return (
    <div className="flex flex-col gap-4">
      {/* Block */}
      <Section title="Блокировки">
        <ToggleRow
          label="Заблокировать рекламу"
          checked={cfg.block_ads}
          onChange={(v) => set({ block_ads: v })}
        />
        <ToggleRow
          label="Заблокировать BitTorrent"
          checked={cfg.block_bittorrent}
          onChange={(v) => set({ block_bittorrent: v })}
        />
        <TagsInput
          label="Заблокированные IP-адреса"
          value={cfg.block_ips}
          onChange={(v) => set({ block_ips: v })}
          options={without(geoipOpts, cfg.warp_ips)}
          placeholder="CIDR или geoip:xx…"
        />
        <TagsInput
          label="Заблокированные домены"
          value={cfg.block_domains}
          onChange={(v) => set({ block_domains: v })}
          options={without(geositeOpts, cfg.warp_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
      </Section>

      {/* Routing order */}
      <Section
        title="Порядок маршрутизации"
        desc="Правила проверяются сверху вниз (блокировки — всегда первыми). Последний пункт — «всё остальное»: туда уходит весь несовпавший трафик."
      >
        <div className="flex flex-col gap-1.5">
          {cfg.routing_order.map((lane, i) => {
            const last = i === cfg.routing_order.length - 1;
            return (
              <div
                key={lane}
                className="flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2"
              >
                <span className="w-5 text-sm font-bold text-ink-muted">
                  {i + 1}
                </span>
                <span className="flex-1 text-sm font-medium text-ink">
                  {laneLabel(lane)}
                  {last && (
                    <span className="ml-2 text-xs font-normal text-ink-muted">
                      · всё остальное
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  disabled={i === 0}
                  onClick={() => moveLane(i, -1)}
                  className="rounded p-1 text-gray-500 hover:bg-gray-200 disabled:opacity-30"
                >
                  <IconChevron className="rotate-180" />
                </button>
                <button
                  type="button"
                  disabled={last}
                  onClick={() => moveLane(i, 1)}
                  className="rounded p-1 text-gray-500 hover:bg-gray-200 disabled:opacity-30"
                >
                  <IconChevron />
                </button>
              </div>
            );
          })}
        </div>
      </Section>

      {/* Direct */}
      <Section title="Напрямую" desc="Эти домены/IP идут напрямую с этого сервера.">
        <TagsInput
          label="Домены"
          value={cfg.direct_domains}
          onChange={(v) => set({ direct_domains: v })}
          options={geositeOpts}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="IP"
          value={cfg.direct_ips}
          onChange={(v) => set({ direct_ips: v })}
          options={geoipOpts}
          placeholder="CIDR или geoip:xx…"
        />
      </Section>

      {/* WARP */}
      <Section
        title={
          <span className="flex items-center gap-2">
            Cloudflare WARP
            <Badge color={warpBadge.color}>{warpBadge.label}</Badge>
          </span>
        }
        desc="Включите, чтобы работали категории «Правила WARP» ниже."
        action={
          <Switch
            checked={warpEnabled}
            disabled={applying}
            onChange={setWarpEnabled}
          />
        }
      >
        <TagsInput
          label="Правила WARP - Домены"
          value={cfg.warp_domains}
          onChange={(v) => set({ warp_domains: v })}
          options={without(geositeOpts, cfg.block_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="Правила WARP — IP"
          value={cfg.warp_ips}
          onChange={(v) => set({ warp_ips: v })}
          options={without(geoipOpts, cfg.block_ips)}
          placeholder="CIDR или geoip:xx…"
        />
      </Section>

      {/* Opera VPN */}
      <Section
        title={
          <span className="flex items-center gap-2">
            Opera VPN
            <Badge color={operaBadge.color}>{operaBadge.label}</Badge>
          </span>
        }
        desc="Бесплатный Opera VPN как отдельный выход. Включите, чтобы работали категории «Правила Opera» ниже."
        action={
          <Switch
            checked={operaEnabled}
            disabled={applying}
            onChange={setOperaEnabled}
          />
        }
      >
        <Select
          label="Регион"
          data={OPERA_COUNTRIES}
          value={operaCountry}
          onChange={setOperaCountry}
        />
        <TagsInput
          label="Правила Opera — Домены"
          value={cfg.opera_domains}
          onChange={(v) => set({ opera_domains: v })}
          options={without(geositeOpts, cfg.block_domains)}
          placeholder="домен, regexp: или geosite:…"
        />
        <TagsInput
          label="Правила Opera — IP"
          value={cfg.opera_ips}
          onChange={(v) => set({ opera_ips: v })}
          options={without(geoipOpts, cfg.block_ips)}
          placeholder="CIDR или geoip:xx…"
        />
      </Section>

      {/* Proxy lanes */}
      <Section
        title="Полосы прокси"
        desc="У каждой полосы свои прокси и свои правила: например, .ru уходит через один, а .com — через другой."
      >
        {cfg.lanes.length === 0 && (
          <p className="rounded-lg border border-dashed border-gray-200 px-3 py-4 text-center text-sm text-ink-muted">
            Полос пока нет.
          </p>
        )}

        {cfg.lanes.map((lane) => {
          const status = laneStatus(lane);
          return (
            <div
              key={lane.id}
              className="flex flex-col gap-4 rounded-xl border border-gray-200 p-3"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <div className="flex items-center gap-2">
                    <Badge color={status.color}>{status.label}</Badge>
                  </div>
                  <TextInput
                    value={lane.name}
                    onChange={(v) => patchLane(lane.id, { name: v })}
                    placeholder="Название полосы, например «Зона .ru»"
                  />
                </div>
                <Switch
                  checked={lane.enabled}
                  disabled={applying}
                  onChange={(v) => patchLane(lane.id, { enabled: v })}
                />
              </div>

              <div>
                <span className="mb-1.5 block text-sm text-ink-muted">
                  Источник прокси
                </span>
                <SegmentedControl
                  value={laneSrc[lane.id] ?? "manual"}
                  onChange={(v) =>
                    setLaneSrc((s) => ({ ...s, [lane.id]: v as LaneSource }))
                  }
                  data={[
                    { value: "manual", label: "Вручную" },
                    { value: "urls", label: "Файлы (URL)" },
                  ]}
                />
              </div>
              {(laneSrc[lane.id] ?? "manual") === "manual" ? (
                <TagsInput
                  label="Прокси вручную"
                  value={lane.manual}
                  onChange={(v) => patchLane(lane.id, { manual: v })}
                  placeholder="socks5://ip:port — добавить и Enter…"
                />
              ) : (
                <TagsInput
                  label="URL-списки прокси"
                  value={lane.urls}
                  onChange={(v) => patchLane(lane.id, { urls: v })}
                  placeholder="https://example.com/proxy.txt — добавить и Enter…"
                />
              )}
              <TagsInput
                label="Домены полосы"
                value={lane.domains}
                onChange={(v) => patchLane(lane.id, { domains: v })}
                options={without(geositeOpts, cfg.block_domains)}
                placeholder="домен, regexp: или geosite:…"
              />
              <TagsInput
                label="IP полосы"
                value={lane.ips}
                onChange={(v) => patchLane(lane.id, { ips: v })}
                options={without(geoipOpts, cfg.block_ips)}
                placeholder="CIDR или geoip:xx…"
              />
              <div className="flex justify-end">
                <Button
                  variant="light"
                  size="sm"
                  onClick={() => removeLane(lane.id)}
                >
                  Удалить полосу
                </Button>
              </div>
            </div>
          );
        })}

        <div className="flex items-center justify-between gap-3">
          <Button
            variant="light"
            size="sm"
            disabled={cfg.lanes.length >= MAX_LANES}
            onClick={addLane}
          >
            + Добавить полосу
          </Button>
          {cfg.lanes.length >= MAX_LANES && (
            <span className="text-xs text-ink-muted">
              максимум {MAX_LANES} полос
            </span>
          )}
        </div>

        {/* One cadence for every URL-sourced lane. */}
        {cfg.lanes.some((l) => laneSrc[l.id] === "urls") && (
          <Select
            label="Авто-обновление URL-списков"
            data={PROXY_REFRESH}
            value={String(cfg.proxy_refresh_minutes)}
            onChange={(v) => set({ proxy_refresh_minutes: Number(v) })}
          />
        )}
      </Section>
    </div>
  );
}
