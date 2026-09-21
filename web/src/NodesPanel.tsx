import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExternalServers } from "./ExternalServers";
import {
  getGeoCategories,
  getMe,
  getSettings,
  listNodes,
  updateAllNodes,
  type GeoCategories,
  type NodeView,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, CenterLoader } from "./ui";
import { AddNodeDialog, InstallCommandModal } from "./AddNodeDialog";
import { NodeCard } from "./NodeCard";
import { agentSkew } from "./NodeStatus";

export function NodesPanel() {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
  // Geo categories feed the routing editor's domain/IP suggestions (same list for
  // the master and every node — one panel-side geosite/geoip).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [], iplist: [] });
  const [adding, setAdding] = useState(false);
  const [installCmd, setInstallCmd] = useState<string | null>(null);
  const [panelVersion, setPanelVersion] = useState("");

  const load = () =>
    listNodes()
      .then((r) => setNodes(r.nodes))
      .catch((e) => notifyError(errMessage(e)));

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load();
    getSettings()
      .then((s) => setDecoys(s.decoy_templates || []))
      .catch(() => {});
    getMe()
      .then((m) => setPanelVersion(m.version || ""))
      .catch(() => {});
    getGeoCategories()
      .then((g) =>
        setGeo({
          geosite: g.geosite ?? [],
          geoip: g.geoip ?? [],
          iplist: g.iplist ?? [],
        }),
      )
      .catch(() => {});
  }, []);

  // A requested restart resolves in a couple of seconds and its outcome is only
  // shown briefly, so the list polls fast for the whole of it — pending AND the
  // answer that follows. Polling only while pending would leave the "Xray
  // restarted badge on screen until the next lazy tick, well past the few
  // seconds the server means it to be shown. Otherwise this is just the liveness
  // refresh keeping online/offline badges current, and it stays lazy.
  const showingRestart = !!nodes?.some((n) => n.xray_restart);
  // biome-ignore lint/correctness/useExhaustiveDependencies: the cadence is the only thing this re-reads; load is redefined every render and would restart the timer on each one
  useEffect(() => {
    // 8s while the page is open. A node's own report lands every 30–60s (the panel
    // holds its long-poll that long), so this cannot make node data fresher than the
    // node makes it — but everything the PANEL knows already, an enable, a queued
    // restart, an agent version after an update, shows up within a tick instead of
    // sitting on screen stale for a quarter of a minute.
    const t = setInterval(load, showingRestart ? 2000 : 8000);
    return () => clearInterval(t);
  }, [showingRestart]);

  if (nodes === null) return <CenterLoader />;

  const remoteCount = nodes.filter((n) => !n.is_local).length;
  // Two different problems, two different fixes: nodes behind the panel are what
  // "Update all" is for, while a node ahead of the panel means the panel is the
  // one to update — telling the operator to pull those "up" would be backwards.
  const anyBehind = nodes.some((n) => n.online && agentSkew(n, panelVersion) === "older");
  const anyAhead = nodes.some((n) => n.online && agentSkew(n, panelVersion) === "newer");

  const updateAll = async () => {
    try {
      const r = await updateAllNodes();
      notifySuccess(t("nodes.updateStarted", { count: r.nodes }));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => setAdding(true)}>
          {t("nodes.addNode")}
        </Button>
        {remoteCount > 0 && (
          <Button size="sm" variant="outline" color="gray" onClick={updateAll}>
            {t("nodes.updateAll")}{anyBehind ? " ⚠" : ""}
          </Button>
        )}
      </div>

      {anyBehind && (
        <p className="accent-tint rounded-lg px-3 py-2 text-xs text-accent">
          {t("nodes.someAgentsOutdated")}
        </p>
      )}

      {anyAhead && (
        <p className="accent-tint rounded-lg px-3 py-2 text-xs text-accent">
          {t("nodes.someAgentsAhead")}
        </p>
      )}

      {/* A vertical list of sections, one per server — not a grid of cards. A fleet is
          read top to bottom, and a row that wraps to a second column is a row an
          operator scrolls past. */}
      {nodes.map((n) => (
        <NodeCard
          key={n.id}
          node={n}
          decoys={decoys}
          geo={geo}
          panelVersion={panelVersion}
          onChanged={load}
          onRegen={setInstallCmd}
        />
      ))}

      {/* One server is not a fleet: say what the page is for instead of leaving the
          master alone under a heading about servers. */}
      {remoteCount === 0 && (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-brand-600/10 bg-white px-4 py-10 text-center">
          <p className="text-sm font-semibold text-ink">{t("nodes.onlyThisServer")}</p>
          <p className="max-w-md text-xs text-ink-muted">{t("nodes.onlyThisServerHint")}</p>
          <Button size="sm" className="mt-2" onClick={() => setAdding(true)}>
            {t("nodes.addNode")}
          </Button>
        </div>
      )}

      <ExternalServers />

      {adding && (
        <AddNodeDialog
          onClose={() => setAdding(false)}
          onCreated={(cmd) => {
            setAdding(false);
            setInstallCmd(cmd);
            load();
          }}
          onDone={() => {
            setAdding(false);
            load();
          }}
        />
      )}
      {installCmd && (
        <InstallCommandModal command={installCmd} onClose={() => setInstallCmd(null)} />
      )}
    </div>
  );
}
