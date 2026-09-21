import { useEffect, useState } from "react";
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
import { Button, cn } from "./ui";

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
