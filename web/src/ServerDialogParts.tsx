import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  getNodeGeo,
  refreshNodeGeo,
  setNodeGeoCadence,
  type GeoInfo,
  type NodeView,
  type RoutingConfig,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  effectiveCfg,
  GeoSection,
  hydrateRouting,
  laneSources,
  type LaneSource,
} from "./RoutingEditor";
import { useCan } from "./role";
import { Badge, Button, cn, IconCheck, IconCopy, Modal, Section, SettingRow } from "./ui";
import { serverName, statusDot } from "./NodeStatus";

// The tabs of a server's settings that belong to routing (routing.*); every other
// tab is the server itself (servers.*).
const ROUTING_TABS = new Set(["routing", "dns", "geo", "iplist"]);

// useServerTabs narrows a server dialog's tabs to the ones the role may see, picks
// the tab actually shown (the asked-for one, or the first visible), and says whether
// that tab is read-only — seen without the matching manage permission.
export function useServerTabs<T extends { value: string }>(tabs: T[], tab: string) {
  const serversView = useCan("servers.view");
  const serversManage = useCan("servers.manage");
  const routingView = useCan("routing.view");
  const routingManage = useCan("routing.manage");
  // General carries the system proxy, which is routing's — so routing.view opens it
  // too (the server's own fields there read-only without servers.manage).
  const visible = tabs.filter((x) =>
    ROUTING_TABS.has(x.value)
      ? routingView
      : x.value === "general"
        ? serversView || routingView
        : serversView,
  );
  const shown = visible.some((x) => x.value === tab) ? tab : (visible[0]?.value ?? tab);
  const readOnly = ROUTING_TABS.has(shown) ? !routingManage : !serversManage;
  return { visible, shown, readOnly };
}

export function DialogTabs({
  tabs,
  value,
  onChange,
}: {
  tabs: { value: string; label: string }[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="no-scrollbar -mx-5 flex gap-0.5 overflow-x-auto px-5">
      {tabs.map((t) => (
        <button
          type="button"
          key={t.value}
          onClick={() => onChange(t.value)}
          className={cn(
            "whitespace-nowrap border-b-2 px-3 py-2.5 text-[13px] font-semibold transition",
            value === t.value
              ? "border-brand-600 text-ink"
              : "border-transparent text-ink-muted hover:text-ink",
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

// TabSaveBar is the inline save row for the "form" tabs (general / routing / DNS)
// of the server dialogs, so each tab commits on its own — exactly like the
// connections / geo / domain tabs already do, staying open after save. Cancel
// reverts unsaved edits to the last-saved state; both buttons disable when there's
// nothing to save. The note spells out that edits are staged and per-section.
export function TabSaveBar({
  onSave,
  onReset,
  dirty,
  busy,
  invalid,
}: {
  onSave: () => void;
  onReset: () => void;
  dirty: boolean;
  busy: boolean;
  // invalid blocks Save only. Cancel stays live on purpose: a form the panel refuses
  // to save is exactly when the operator most needs a way back to the saved state.
  invalid?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-2 border-t border-gray-200 pt-4 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-ink-muted">
        {t("conn.saveHint")}
      </p>
      <div className="flex justify-end gap-2">
        <Button
          variant="light"
          color="gray"
          onClick={onReset}
          disabled={!dirty || busy}
        >
          {t("common.cancel")}
        </Button>
        <Button onClick={onSave} loading={busy} disabled={!dirty || invalid}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}

// statusDot is the colour of the small dot that leads each server row. It answers
// "is this server serving users", not "is it answering us" — a node whose agent
// syncs every few seconds while its Xray is dead carries nobody, and painting that
// green put a green dot next to the red alert describing the very same server.
//
//	green  — up and serving
//	amber  — on the wire, but its Xray is not running
//	red    — enabled and installed, and we have not heard from it
//	grey   — switched off, or never installed
//
// Exported because the dashboard's fleet strip shows the same servers — two places
// deciding independently what "up" looks like is how they end up disagreeing about
// the same node.
// clampCoefficient parses the traffic-multiplier field and holds it in the range the
// server accepts (0.1–10), defaulting a blank or unparseable value to the neutral 1.
export function clampCoefficient(v: string): number {
  const n = parseFloat(v);
  if (!isFinite(n) || n <= 0) return 1;
  return Math.min(Math.max(n, 0.1), 10);
}

// nodeDefaultRouting is a fresh node routing override: the full editor's default
// (block/direct/lanes/WARP/Opera), with ad-blocking on — the operator just enabled
// "own routing", so give them a sensible starting point.
export function nodeDefaultRouting(): RoutingConfig {
  return { ...hydrateRouting(null), block_ads: true };
}

// useServerRouting holds the editable routing + egress state shared by the node and
// master settings dialogs, so both drive the same RoutingEditor. The container owns
// saving; this only manages the in-progress edit (and lane-source flip state).
export function useServerRouting(init: {
  cfg: RoutingConfig;
  warp: boolean;
  opera: boolean;
  country: string;
}) {
  const [cfg, setCfg] = useState<RoutingConfig>(init.cfg);
  const [laneSrc, setLaneSrc] = useState<Record<string, LaneSource>>(() =>
    laneSources(init.cfg.lanes),
  );
  const [warpEnabled, setWarpEnabled] = useState(init.warp);
  const [operaEnabled, setOperaEnabled] = useState(init.opera);
  const [operaCountry, setOperaCountry] = useState(init.country || "EU");
  // base is the last-saved snapshot, for dirty-tracking and Cancel (revert).
  // Seeded from init, re-seeded on reset (master's async load), refreshed on commit
  // (after a successful save).
  const [base, setBase] = useState({
    cfg: init.cfg,
    warp: init.warp,
    opera: init.opera,
    country: init.country || "EU",
  });
  const snap = (c: RoutingConfig, w: boolean, o: boolean, cc: string) =>
    JSON.stringify({ c: effectiveCfg(c, laneSources(c.lanes)), w, o, cc });
  const dirty =
    JSON.stringify({ c: effectiveCfg(cfg, laneSrc), w: warpEnabled, o: operaEnabled, cc: operaCountry }) !==
    snap(base.cfg, base.warp, base.opera, base.country);
  return {
    cfg,
    onCfg: (patch: Partial<RoutingConfig>) => setCfg((c) => ({ ...c, ...patch })),
    laneSrc,
    setLaneSrc,
    warpEnabled,
    setWarpEnabled,
    operaEnabled,
    setOperaEnabled,
    operaCountry,
    setOperaCountry,
    // What the server currently has, as against the draft above: a status word must
    // describe the running egress, not a switch nobody has saved yet.
    savedWarp: base.warp,
    savedOpera: base.opera,
    effective: () => effectiveCfg(cfg, laneSrc),
    dirty,
    // revert restores the editor to the last-saved snapshot.
    revert: () => {
      setCfg(base.cfg);
      setLaneSrc(laneSources(base.cfg.lanes));
      setWarpEnabled(base.warp);
      setOperaEnabled(base.opera);
      setOperaCountry(base.country);
    },
    // commit marks the current state as saved (call after a successful save).
    commit: () =>
      setBase({
        cfg: effectiveCfg(cfg, laneSrc),
        warp: warpEnabled,
        opera: operaEnabled,
        country: operaCountry,
      }),
    // load replaces the draft only, leaving the baseline: the editor turns dirty and
    // Cancel brings the saved state back (copying another server's routing).
    load: (c: RoutingConfig, w: boolean, o: boolean, cc: string) => {
      setCfg(c);
      setLaneSrc(laneSources(c.lanes));
      setWarpEnabled(w);
      setOperaEnabled(o);
      setOperaCountry(cc || "EU");
    },
    // reset re-seeds every field AND the baseline (used by the master dialog after its
    // async load, so a freshly-loaded editor is not "dirty").
    reset: (c: RoutingConfig, w: boolean, o: boolean, cc: string) => {
      setCfg(c);
      setLaneSrc(laneSources(c.lanes));
      setWarpEnabled(w);
      setOperaEnabled(o);
      setOperaCountry(cc || "EU");
      setBase({ cfg: c, warp: w, opera: o, country: cc || "EU" });
    },
  };
}

// copiedRouting is another server's routing as this server would run it: a cascade
// exit pointing at this very server is dropped (a server cannot leave through
// itself), and a cascade lane left with no exit goes with it. dropped names those.
export function copiedRouting(
  src: NodeView,
  self: number,
): { cfg: RoutingConfig; dropped: string[] } {
  const c = hydrateRouting(src.routing);
  const dropped: string[] = [];
  const lanes = c.lanes.flatMap((l) => {
    if (!l.via?.length) return [l];
    const via = l.via.filter((e) => e.server !== self);
    if (via.length === 0) {
      dropped.push(l.name || l.id);
      return [];
    }
    return [{ ...l, via }];
  });
  return { cfg: hydrateRouting({ ...c, lanes }), dropped };
}

// CopyRoutingFrom opens a dialog to pick another server, see what its routing
// holds, and load it into the editor. Nothing is saved: the tab turns dirty, Save
// applies, Cancel undoes.
export function CopyRoutingFrom({
  self,
  peers,
  busy,
  onCopy,
}: {
  self: number;
  peers: NodeView[];
  // A save in flight: a draft loaded now would be marked saved by its commit.
  busy?: boolean;
  onCopy: (cfg: RoutingConfig, warp: boolean, opera: boolean, country: string) => void;
}) {
  const { t } = useTranslation();
  const others = peers.filter((p) => p.id !== self);
  const [open, setOpen] = useState(false);
  const [from, setFrom] = useState<number | null>(null);
  if (others.length === 0) return null;
  const src = others.find((p) => p.id === from);
  const preview = src ? copiedRouting(src, self) : null;
  const close = () => {
    setOpen(false);
    setFrom(null);
  };
  const copy = () => {
    if (!src || !preview) return;
    onCopy(preview.cfg, src.warp_enabled, src.opera_enabled, src.opera_country);
    notifySuccess(t("nodes.routingCopied", { name: serverName(src) }));
    close();
  };
  const c = preview?.cfg;
  return (
    <>
      <Section flush>
        <SettingRow
          label={t("nodes.copyRoutingFrom")}
          hint={t("nodes.copyRoutingHint")}
          control={
            <Button
              size="sm"
              variant="outline"
              color="gray"
              onClick={() => setOpen(true)}
              disabled={busy}
            >
              <span className="inline-flex items-center gap-1.5">
                <IconCopy size={14} />
                {t("nodes.copyRoutingOpen")}
              </span>
            </Button>
          }
        />
      </Section>
      <Modal
        open={open}
        onClose={close}
        size="lg"
        title={t("nodes.copyRoutingTitle")}
        subtitle={t("nodes.copyRoutingHint")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={close}>
              {t("common.cancel")}
            </Button>
            <Button onClick={copy} disabled={!preview}>
              {t("nodes.copyRoutingApply")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-5">
          <div className="flex flex-col gap-2">
            <span className="text-xs font-semibold text-ink-muted">
              {t("nodes.copyRoutingSource")}
            </span>
            <ServerPicker
              servers={others}
              value={from}
              onChange={setFrom}
              badges={(p) => {
                const lanes = copiedRouting(p, self).cfg.lanes.length;
                return (
                  <>
                    <Badge size="xs" color={p.warp_enabled ? "green" : "gray"}>WARP</Badge>
                    <Badge size="xs" color={p.opera_enabled ? "green" : "gray"}>Opera</Badge>
                    <Badge size="xs" color={lanes ? "brand" : "gray"}>
                      {t("nodes.copyLanes", { count: lanes })}
                    </Badge>
                  </>
                );
              }}
            />
          </div>

          {src && c && preview ? (
            <div className="flex flex-col gap-2">
              <span className="text-xs font-semibold text-ink-muted">
                {t("nodes.copyRoutingWhat")}
              </span>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <RuleTile
                  label={t("route.blocks")}
                  domains={c.block_domains.length}
                  ips={c.block_ips.length}
                  flags={[
                    c.block_ads && t("nodes.copyAds"),
                    c.block_bittorrent && "BitTorrent",
                  ]}
                />
                <RuleTile
                  label={t("route.direct")}
                  domains={c.direct_domains.length}
                  ips={c.direct_ips.length}
                />
                <RuleTile
                  label="WARP"
                  state={src.warp_enabled}
                  domains={c.warp_domains.length}
                  ips={c.warp_ips.length}
                />
                <RuleTile
                  label="Opera VPN"
                  state={src.opera_enabled}
                  extra={src.opera_enabled ? src.opera_country || "EU" : undefined}
                  domains={c.opera_domains.length}
                  ips={c.opera_ips.length}
                />
              </div>
              <div className="rounded-xl border border-gray-200 px-3 py-2.5">
                <div className="mb-1.5 text-xs text-ink-muted">{t("route.lanes")}</div>
                {c.lanes.length ? (
                  <div className="flex flex-wrap gap-1.5">
                    {c.lanes.map((l) => (
                      <span
                        key={l.id}
                        className={cn(
                          "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs",
                          l.enabled ? "border-gray-200 text-ink" : "border-dashed border-gray-300 text-ink-muted",
                        )}
                      >
                        <span className={cn("h-1.5 w-1.5 rounded-full", l.enabled ? "bg-success" : "bg-gray-300")} />
                        {l.name || l.id}
                      </span>
                    ))}
                  </div>
                ) : (
                  <span className="text-[13px] text-ink-muted">—</span>
                )}
              </div>
              {preview.dropped.length > 0 && (
                <p className="warning-tint rounded-lg px-3 py-2 text-xs text-warning">
                  {t("nodes.copyRoutingDropped", { names: preview.dropped.join(", ") })}
                </p>
              )}
            </div>
          ) : (
            <p className="rounded-xl border border-dashed border-gray-200 px-3 py-6 text-center text-xs text-ink-muted">
              {t("nodes.copyRoutingPick")}
            </p>
          )}
        </div>
      </Modal>
    </>
  );
}

// ServerPicker is the server choice of the copy dialogs: one card per server with
// its status dot, name, address and whatever badges the caller shows.
export function ServerPicker({
  servers,
  value,
  onChange,
  badges,
}: {
  servers: NodeView[];
  value: number | null;
  onChange: (id: number) => void;
  badges?: (s: NodeView) => ReactNode;
}) {
  return (
    <div className="grid gap-2 sm:grid-cols-2" role="radiogroup">
      {servers.map((p) => {
        const on = p.id === value;
        return (
          <button
            key={p.id}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange(p.id)}
            className={cn(
              "flex min-w-0 items-start gap-3 rounded-xl border px-3 py-2.5 text-start transition",
              "outline-none focus-visible:ring-2 focus-visible:ring-brand-500/40",
              on
                ? "border-accent accent-tint"
                : "border-gray-200 bg-white hover:border-gray-300 hover:bg-gray-50",
            )}
          >
            <span className={cn("mt-1.5 h-2 w-2 shrink-0 rounded-full", statusDot(p))} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-semibold text-ink">
                {serverName(p)}
              </span>
              <span className="block truncate text-xs text-ink-muted" dir="ltr">
                {p.host || "—"}
              </span>
              {badges && <span className="mt-1.5 flex flex-wrap gap-1">{badges(p)}</span>}
            </span>
            <span
              className={cn(
                "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border transition",
                on ? "border-brand-600 bg-brand-600 text-onbrand" : "border-gray-300 bg-white",
              )}
            >
              {on && <IconCheck size={11} />}
            </span>
          </button>
        );
      })}
    </div>
  );
}

// RuleTile is one rule group in the copy preview: its switch (when it has one), and
// how many domain and IP rules it carries.
function RuleTile({
  label,
  domains,
  ips,
  state,
  extra,
  flags,
}: {
  label: string;
  domains: number;
  ips: number;
  state?: boolean;
  extra?: string;
  flags?: (string | false)[];
}) {
  const { t } = useTranslation();
  const off = state === false;
  const shown = (flags ?? []).filter(Boolean) as string[];
  return (
    <div className={cn("flex flex-col gap-2 rounded-xl border border-gray-200 px-3 py-2.5", off && "bg-gray-50")}>
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-semibold text-ink">{label}</span>
        {state !== undefined && (
          <Badge size="xs" color={state ? "green" : "gray"}>
            {state ? (extra ?? t("nodes.on")) : t("conn.off")}
          </Badge>
        )}
      </div>
      <div className={cn("flex gap-4", off && "opacity-60")}>
        <Count n={domains} label={t("route.domains")} />
        <Count n={ips} label="IP" />
      </div>
      {shown.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {shown.map((f) => (
            <Badge key={f} size="xs" color="red">{f}</Badge>
          ))}
        </div>
      )}
    </div>
  );
}

function Count({ n, label }: { n: number; label: string }) {
  return (
    <span className="flex flex-col">
      <span className={cn("font-mono text-lg leading-none tabular-nums", n ? "text-ink" : "text-ink-muted")}>{n}</span>
      <span className="mt-1 text-[11px] text-ink-muted">{label}</span>
    </span>
  );
}

// NodeGeoCard is the node's Geo tab — the same GeoSection as the master (geo file
// status + auto-refresh cadence), but scoped to the node: files come from the node's
// report, the refresh button queues a refresh on the node, and the cadence is the node's own.
export function NodeGeoCard({ node, onChanged }: { node: NodeView; onChanged: () => void }) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<GeoInfo | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getNodeGeo(node.id).then(setInfo).catch(() => {});
  }, [node.id]);

  const refresh = async () => {
    setBusy(true);
    try {
      await refreshNodeGeo(node.id);
      notifySuccess(t("nodes.geoQueued"));
    } catch (e) {
      notifyError(errMessage(e));
    }
    setBusy(false);
  };

  const changeCadence = async (hours: number) => {
    const prev = info?.refresh_hours ?? node.geo_refresh_hours;
    setInfo((i) => (i ? { ...i, refresh_hours: hours } : i));
    try {
      await setNodeGeoCadence(node.id, hours);
      notifySuccess(t("nodes.geoCadenceSaved"));
      onChanged();
    } catch (e) {
      // Roll the optimistic update back so the dropdown doesn't misreport the cadence.
      setInfo((i) => (i ? { ...i, refresh_hours: prev } : i));
      notifyError(errMessage(e));
    }
  };

  return (
    <GeoSection
      status={info?.files ?? []}
      onRefresh={refresh}
      refreshing={busy}
      cadence={info?.refresh_hours ?? node.geo_refresh_hours}
      onCadence={changeCadence}
    />
  );
}
