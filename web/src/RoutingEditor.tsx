import { useMemo, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n, { currentLang } from "./i18n";
import { type EgressLane, type GeoFile, type RoutingConfig } from "./api";
import { fmtBytes } from "./format";
import {
  Badge,
  Button,
  cn,
  Code,
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

// directStrategies() are the ways the direct outbound may resolve a name before
// dialling it (Xray's freedom domainStrategy). The default is Xray's own — naming
// a family instead makes the panel's DNS decide and pins the address family, which
// is what fixes "only through the tunnel some sites crawl" on a host whose IPv6
// route is broken.
// AsIs is not offered separately: it IS Xray's default, so listing both gave the
// same behaviour two names. A config that carries it explicitly (set through the
// API) is shown as the default — see fromRouting.
export const directStrategies = (): Opt[] => [
  { value: "", label: i18n.t("route.stratDefault") },
  { value: "UseIP", label: i18n.t("route.stratUseIP") },
  { value: "UseIPv4", label: i18n.t("route.stratUseIPv4") },
  { value: "UseIPv6", label: i18n.t("route.stratUseIPv6") },
  { value: "UseIPv4v6", label: i18n.t("route.stratUseIPv4v6") },
  { value: "UseIPv6v4", label: i18n.t("route.stratUseIPv6v4") },
];

// proxyRefresh() are the URL auto-refresh cadence options (minutes; -1 = never).
export const proxyRefresh = (): Opt[] => [
  { value: "30", label: i18n.t("route.every30m") },
  { value: "60", label: i18n.t("route.every1h") },
  { value: "180", label: i18n.t("route.every3h") },
  { value: "360", label: i18n.t("route.every6h") },
  { value: "720", label: i18n.t("route.every12h") },
  { value: "-1", label: i18n.t("subs.never") },
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
  direct_strategy: "",
  routing_order: ["warp", "opera", "direct"],
  lanes: [],
  proxy_refresh_minutes: 30,
};

// builtinLaneName labels the always-present lanes in the routing-order card.
// A proxy lane is labelled by its own name instead.
const builtinLaneName = (lane: string): string =>
  lane === "direct" ? i18n.t("route.direct") : lane === "warp" ? "WARP" : "Opera VPN";

// Opera VPN regions opera-proxy supports.
export const operaCountries = () => [
  { value: "EU", label: i18n.t("route.europe") },
  { value: "AS", label: i18n.t("route.asia") },
  { value: "AM", label: i18n.t("route.america") },
];

// BUILTIN_LANES are the lanes that always exist, in default precedence. Mirrors
// model.BuiltinLanes in internal/model/model.go.
const BUILTIN_LANES = ["warp", "opera", "direct"];

// MAX_LANES mirrors model.MaxEgressLanes.
const MAX_LANES = 16;

// fmtWhen renders a unix timestamp as a local date+time, or a dash when unset.
const fmtWhen = (unix: number) =>
  unix
    ? new Date(unix * 1000).toLocaleString(currentLang(), {
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
    // "AsIs" and "" mean the same to Xray; the editor shows one of them.
    direct_strategy: src.direct_strategy === "AsIs" ? "" : (src.direct_strategy ?? ""),
    lanes,
    routing_order: normalizeOrder(
      src.routing_order,
      lanes.map((l) => l.id),
    ),
    // 0 (absent / pre-feature default) shows as 30; -1 stays "never".
    proxy_refresh_minutes: src.proxy_refresh_minutes || 30,
  };
}

// geoCadence() are the geo auto-refresh options (hours; 0 = never).
export const geoCadence = (): Opt[] => [
  { value: "0", label: i18n.t("route.neverManual") },
  { value: "24", label: i18n.t("route.onceADay") },
  { value: "72", label: i18n.t("route.every3Days") },
  { value: "168", label: i18n.t("route.onceAWeek") },
];

// iplistCadence() are the iplist auto-refresh options. They get their own list —
// and a 12-hour step the geo one has no use for — because the iplist services
// re-resolve their addresses about every 12 hours, so polling them daily already
// lags a full cycle behind.
export const iplistCadence = (): Opt[] => [
  { value: "0", label: i18n.t("route.neverManual") },
  { value: "12", label: i18n.t("route.every12hOnce") },
  { value: "24", label: i18n.t("route.onceADay") },
  { value: "72", label: i18n.t("route.every3Days") },
  { value: "168", label: i18n.t("route.onceAWeek") },
];

// FileRow reports one database's on-disk state, with an optional note beneath it.
//
// `label` overrides what leads the row. The Geo tab leaves it alone — there the
// file IS the thing, "geoip.dat" is what Xray loads. The iplist tab passes the
// source name instead ("global"), because the cache filename is an implementation
// detail the operator never types: what they write in a rule is iplist:global/….
//
// It stacks below sm: the label plus its size and date do not fit on one
// phone-width line, and side-by-side the flex row squeezed the name until it
// broke mid-word. Above sm they sit on one line, name left, meta right.
function FileRow({
  file,
  label,
  note,
}: {
  file: GeoFile;
  label?: string;
  note?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-2">
        <span className="break-all font-mono text-xs text-ink">
          {label ?? file.name}
        </span>
        <span className="text-xs text-ink-muted sm:shrink-0">
          {file.present
            ? `${fmtBytes(file.size)} · ${t("route.updatedAt", { when: fmtWhen(file.modified_at) })}`
            : t("route.noFile")}
        </span>
      </div>
      {note}
    </div>
  );
}

// GeoFileRows reports one database set's on-disk state. Shared by the Geo and
// iplist tabs so both read identically.
function GeoFileRows({ status }: { status: GeoFile[] }) {
  return (
    <div className="flex flex-col gap-2 text-sm">
      {status.map((f) => (
        <FileRow key={f.name} file={f} />
      ))}
    </div>
  );
}

// CadenceSelect is one database set's auto-refresh schedule. Each set has its own
// (see iplistCadence()), so the option list is passed in rather than assumed.
function CadenceSelect({
  cadence,
  onCadence,
  options,
}: {
  cadence: number;
  onCadence: (hours: number) => void;
  options: Opt[];
}) {
  const { t } = useTranslation();
  return (
    <Select
      label={t("route.autoUpdate")}
      data={options}
      value={String(cadence)}
      onChange={(v) => onCadence(Number(v))}
    />
  );
}

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
  const { t } = useTranslation();
  return (
    // The description goes in the BODY, not Section's `desc` slot: that slot sits
    // beside the action button, which on a phone leaves it ~180px and wraps a plain
    // sentence into a four-line column. As a child it gets the full width.
    <Section
      title={t("route.geoDbs")}
      action={
        <Button variant="light" size="sm" loading={refreshing} onClick={onRefresh}>
          {t("common.refresh")}
        </Button>
      }
    >
      <p className="text-sm text-ink-muted">
        {t("route.geoHint")}
      </p>
      <GeoFileRows status={status} />
      <CadenceSelect cadence={cadence} onCadence={onCadence} options={geoCadence()} />
    </Section>
  );
}

// IPLIST_SOURCES ties each cached file to the source it came from: the name used
// in rules ("global"), the service serving it, and what it holds. Mirrors the
// `sources` and `ipListFiles` maps in internal/geo/geo.go — keep in step.
const IPLIST_SOURCES: {
  file: string;
  source: string; // the name a rule references: iplist:<source>/<group>
  host: string;
  url: string;
  about: string;
}[] = [
  {
    file: "iplist-global.json",
    source: "global",
    host: "iplist.my-handbook.ru",
    url: "https://iplist.my-handbook.ru",
    about: "route.iplistGlobalAbout",
  },
  {
    file: "iplist-russia.json",
    source: "russia",
    host: "russia.iplist.opencck.org",
    url: "https://russia.iplist.opencck.org",
    about: "route.iplistRussiaAbout",
  },
];

// IPListSection is the iplist tab: what the lists are, where they come from, their
// on-disk state, a manual refresh and the (shared) cadence. Deliberately a sibling
// of GeoSection rather than part of it — the two sets look alike but are different
// things: Xray reads the .dat files at runtime, whereas the panel resolves
// "iplist:" rules itself at config-generation time and ships the result.
export function IPListSection({
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
  const { t } = useTranslation();
  const byFile = (name: string) => IPLIST_SOURCES.find((s) => s.file === name);
  return (
    // Description in the body rather than Section's `desc` slot — see GeoSection.
    <Section
      title={t("route.iplists")}
      action={
        <Button variant="light" size="sm" loading={refreshing} onClick={onRefresh}>
          {t("common.refresh")}
        </Button>
      }
    >
      <p className="text-sm text-ink-muted">
        {t("route.iplistHint")}
      </p>

      <div className="flex flex-col gap-3">
        {status.map((f) => {
          const src = byFile(f.name);
          return (
            <FileRow
              key={f.name}
              file={f}
              label={src?.source}
              note={
                src && (
                  <p className="text-xs text-ink-muted">
                    <a
                      href={src.url}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="underline decoration-dotted underline-offset-2 hover:text-ink"
                    >
                      {src.host}
                    </a>{" "}
                    · {t(src.about as "route.iplistGlobalAbout")}
                  </p>
                )
              }
            />
          );
        })}
      </div>

      <div className="flex flex-col gap-2 rounded-lg border border-gray-200 bg-white/60 px-3 py-2 text-xs text-ink-muted">
        <p>
          <Trans
            i18nKey="route.iplistUsage"
            components={{ m: <span className="font-mono" /> }}
          />
        </p>
        <p>
          {t("route.iplistSources")}{" "}
          <a
            href="https://github.com/rekryt/iplist"
            target="_blank"
            rel="noreferrer noopener"
            className="underline decoration-dotted underline-offset-2 hover:text-ink"
          >
            rekryt/iplist
          </a>
          {t("route.iplistSourcesTail")}
        </p>
      </div>

      <CadenceSelect cadence={cadence} onCadence={onCadence} options={iplistCadence()} />
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
// LocalEgressAddress shows the loopback address that reaches this lane, so an
// operator can send something else down it — the Telegram proxy field being the
// reason it exists. Rendered only when the lane is actually up: an address for a
// switched-off egress is a dead port dressed as an instruction.
function LocalEgressAddress({ url }: { url?: string }) {
  if (!url) return null;
  return (
    <Code copy block>
      {url}
    </Code>
  );
}

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
  warpProxyURL,
  operaProxyURL,
  proxyCounts,
  geosite,
  geoip,
  iplist,
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
  // Loopback address that reaches this lane, "" when it is off or when this editor is
  // showing a node (whose addresses are only dialable on that node).
  warpProxyURL?: string;
  operaProxyURL?: string;
  proxyCounts: Record<string, number>;
  geosite: string[];
  geoip: string[];
  iplist: string[];
  applying: boolean;
  liveStatus?: boolean;
}) {
  const { t } = useTranslation();
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
    (["direct", "warp", "opera"].includes(id) ? builtinLaneName(id) : null) ??
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
      name: i18n.t("route.laneN", { n: cfg.lanes.length + 1 }),
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
  //
  // The iplist groups ("iplist:russia/vk") are offered in BOTH: the same ref
  // resolves to the group's domains in a domain field and to its CIDRs in an IP
  // field, so pick it in both to route a service by name and by address.
  const iplistOpts = useMemo<Opt[]>(
    () => iplist.map((g) => ({ value: `iplist:${g}`, label: `iplist: ${g}` })),
    [iplist],
  );
  const geositeOpts = useMemo<Opt[]>(
    () => [...geosite.map((c) => ({ value: `geosite:${c}`, label: c })), ...iplistOpts],
    [geosite, iplistOpts],
  );
  const geoipOpts = useMemo<Opt[]>(
    () => [...geoip.map((c) => ({ value: `geoip:${c}`, label: c })), ...iplistOpts],
    [geoip, iplistOpts],
  );
  // Routing is first-match-wins, so the same matcher claimed by two categories
  // leaves the loser dead — offer each preset only where it is still free. These
  // are every value currently spoken for, across every category of that kind.
  const usedDomains = useMemo(
    () =>
      new Set([
        ...cfg.block_domains,
        ...cfg.warp_domains,
        ...cfg.opera_domains,
        ...cfg.direct_domains,
        ...cfg.lanes.flatMap((l) => l.domains),
      ]),
    [cfg],
  );
  const usedIPs = useMemo(
    () =>
      new Set([
        ...cfg.block_ips,
        ...cfg.warp_ips,
        ...cfg.opera_ips,
        ...cfg.direct_ips,
        ...cfg.lanes.flatMap((l) => l.ips),
      ]),
    [cfg],
  );

  // free hides what ANOTHER category took, keeping `own`'s values: TagsInput
  // already drops selected values from the dropdown, and it reads `options` for
  // their labels — filtering them out would strip a chip's friendly name.
  const free = (opts: Opt[], used: Set<string>, own: string[]) => {
    const mine = new Set(own);
    return opts.filter((o) => !used.has(o.value) || mine.has(o.value));
  };
  const domainOpts = (own: string[]) => free(geositeOpts, usedDomains, own);
  const ipOpts = (own: string[]) => free(geoipOpts, usedIPs, own);

  // The last lane in the routing order is the catch-all: everything unmatched
  // already goes there, so the generator deliberately emits no rules of its own
  // for it (see compileRouting in xray/generate.go). Say so next to the inputs —
  // rules that are silently discarded are how an operator concludes the panel is
  // broken. "direct" is last by DEFAULT, so this is the common case, not an edge.
  //
  // The note must also say what to DO. The trap isn't the redundancy (listing a
  // domain in the catch-all changes nothing, harmlessly) — it's that a lane ABOVE
  // claiming that domain wins, and no rule added here can take it back. Only
  // moving this lane up the order can.
  const catchAll = cfg.routing_order[cfg.routing_order.length - 1];
  const CATCH_ALL_NOTE =
    i18n.t("route.catchAllNote");
  const withCatchAllNote = (desc: string, lane: string) =>
    lane === catchAll ? `${desc} ${CATCH_ALL_NOTE}` : desc;

  // laneStatus counts the proxies the lane RESOLVED (manual entries + whatever its
  // URL sources served). It is NOT a liveness signal. On a node the panel can't see
  // the remote count, so we only show enabled/disabled there (liveStatus=false).
  const laneStatus = (lane: EgressLane): StatusBadge => {
    if (!lane.enabled) return { label: t("route.laneOff"), color: "gray" };
    if (!liveStatus) return { label: t("route.laneOn"), color: "green" };
    const n = proxyCounts[lane.id] ?? 0;
    return n > 0
      ? { label: t("route.nProxies", { count: n }), color: "green" }
      : { label: t("route.noProxies"), color: "orange" };
  };

  return (
    <div className="flex flex-col gap-4">
      {/* Block */}
      <Section title={t("route.blocks")}>
        <ToggleRow
          label={t("route.blockAds")}
          checked={cfg.block_ads}
          onChange={(v) => set({ block_ads: v })}
        />
        <ToggleRow
          label={t("route.blockTorrent")}
          checked={cfg.block_bittorrent}
          onChange={(v) => set({ block_bittorrent: v })}
        />
        <TagsInput
          label={t("route.blockedIps")}
          value={cfg.block_ips}
          onChange={(v) => set({ block_ips: v })}
          options={ipOpts(cfg.block_ips)}
          placeholder={t("route.ipPlaceholder")}
        />
        <TagsInput
          label={t("route.blockedDomains")}
          value={cfg.block_domains}
          onChange={(v) => set({ block_domains: v })}
          options={domainOpts(cfg.block_domains)}
          placeholder={t("route.domainPlaceholder")}
        />
      </Section>

      {/* Routing order */}
      <Section
        title={t("route.order")}
        desc={t("route.orderHint")}
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
                      · {t("route.everythingElse")}
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
      <Section title={t("route.direct")} desc={withCatchAllNote(t("route.directHint"), "direct")}>
        <Select
          label={t("route.directStrategy")}
          data={directStrategies()}
          value={cfg.direct_strategy ?? ""}
          onChange={(v) => set({ direct_strategy: v })}
        />
        <p className="-mt-1 text-xs text-ink-muted">{t("route.directStrategyHint")}</p>
        <TagsInput
          label={t("route.domains")}
          value={cfg.direct_domains}
          onChange={(v) => set({ direct_domains: v })}
          options={domainOpts(cfg.direct_domains)}
          placeholder={t("route.domainPlaceholder")}
        />
        <TagsInput
          label="IP"
          value={cfg.direct_ips}
          onChange={(v) => set({ direct_ips: v })}
          options={ipOpts(cfg.direct_ips)}
          placeholder={t("route.ipPlaceholder")}
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
        desc={withCatchAllNote(t("route.warpHint"), "warp")}
        action={
          <Switch
            checked={warpEnabled}
            disabled={applying}
            onChange={setWarpEnabled}
          />
        }
      >
        <TagsInput
          label={t("route.warpDomains")}
          value={cfg.warp_domains}
          onChange={(v) => set({ warp_domains: v })}
          options={domainOpts(cfg.warp_domains)}
          placeholder={t("route.domainPlaceholder")}
        />
        <TagsInput
          label={t("route.warpIps")}
          value={cfg.warp_ips}
          onChange={(v) => set({ warp_ips: v })}
          options={ipOpts(cfg.warp_ips)}
          placeholder={t("route.ipPlaceholder")}
        />
        <LocalEgressAddress url={warpProxyURL} />
      </Section>

      {/* Opera VPN */}
      <Section
        title={
          <span className="flex items-center gap-2">
            Opera VPN
            <Badge color={operaBadge.color}>{operaBadge.label}</Badge>
          </span>
        }
        desc={withCatchAllNote(t("route.operaHint"), "opera")}
        action={
          <Switch
            checked={operaEnabled}
            disabled={applying}
            onChange={setOperaEnabled}
          />
        }
      >
        <Select
          label={t("route.region")}
          data={operaCountries()}
          value={operaCountry}
          onChange={setOperaCountry}
        />
        <TagsInput
          label={t("route.operaDomains")}
          value={cfg.opera_domains}
          onChange={(v) => set({ opera_domains: v })}
          options={domainOpts(cfg.opera_domains)}
          placeholder={t("route.domainPlaceholder")}
        />
        <TagsInput
          label={t("route.operaIps")}
          value={cfg.opera_ips}
          onChange={(v) => set({ opera_ips: v })}
          options={ipOpts(cfg.opera_ips)}
          placeholder={t("route.ipPlaceholder")}
        />
        <LocalEgressAddress url={operaProxyURL} />
      </Section>

      {/* Proxy lanes */}
      <Section
        title={t("route.lanes")}
        desc={t("route.lanesHint")}
      >
        {cfg.lanes.length === 0 && (
          <p className="rounded-lg border border-dashed border-gray-200 px-3 py-4 text-center text-sm text-ink-muted">
            {t("route.noLanes")}
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
                    placeholder={t("route.laneNamePlaceholder")}
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
                  {t("route.proxySource")}
                </span>
                <SegmentedControl
                  value={laneSrc[lane.id] ?? "manual"}
                  onChange={(v) =>
                    setLaneSrc((s) => ({ ...s, [lane.id]: v as LaneSource }))
                  }
                  data={[
                    { value: "manual", label: t("userDetail.manual") },
                    { value: "urls", label: t("route.filesUrls") },
                  ]}
                />
              </div>
              {(laneSrc[lane.id] ?? "manual") === "manual" ? (
                <TagsInput
                  label={t("route.proxiesManual")}
                  value={lane.manual}
                  onChange={(v) => patchLane(lane.id, { manual: v })}
                  placeholder={t("route.proxyPlaceholder")}
                />
              ) : (
                <TagsInput
                  label={t("route.proxyUrlLists")}
                  value={lane.urls}
                  onChange={(v) => patchLane(lane.id, { urls: v })}
                  placeholder={t("route.proxyUrlPlaceholder")}
                />
              )}
              {lane.id === catchAll && (
                <p className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900">
                  {CATCH_ALL_NOTE}
                </p>
              )}
              <TagsInput
                label={t("route.laneDomains")}
                value={lane.domains}
                onChange={(v) => patchLane(lane.id, { domains: v })}
                options={domainOpts(lane.domains)}
                placeholder={t("route.domainPlaceholder")}
              />
              <TagsInput
                label={t("route.laneIps")}
                value={lane.ips}
                onChange={(v) => patchLane(lane.id, { ips: v })}
                options={ipOpts(lane.ips)}
                placeholder={t("route.ipPlaceholder")}
              />
              <div className="flex justify-end">
                <Button
                  variant="light"
                  size="sm"
                  onClick={() => removeLane(lane.id)}
                >
                  {t("route.deleteLane")}
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
            + {t("route.addLane")}
          </Button>
          {cfg.lanes.length >= MAX_LANES && (
            <span className="text-xs text-ink-muted">
              {t("route.maxLanes", { count: MAX_LANES })}
            </span>
          )}
        </div>

        {/* One cadence for every URL-sourced lane. */}
        {cfg.lanes.some((l) => laneSrc[l.id] === "urls") && (
          <Select
            label={t("route.autoRefreshUrls")}
            data={proxyRefresh()}
            value={String(cfg.proxy_refresh_minutes)}
            onChange={(v) => set({ proxy_refresh_minutes: Number(v) })}
          />
        )}
      </Section>
    </div>
  );
}
