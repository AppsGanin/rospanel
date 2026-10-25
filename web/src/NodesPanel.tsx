import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExternalServers } from "./ExternalServers";
import {
  applyUpdate,
  checkUpdate,
  getGeoCategories,
  getMe,
  getReachConfig,
  getSettings,
  listNodes,
  updateAllNodes,
  updateNodeVersion,
  type GeoCategories,
  type NodeView,
} from "./api";
import { errMessage, notifyError } from "./notify";
import { Button, CenterLoader, Modal, ReadOnly, Spinner, useConfirm } from "./ui";
import { type NodeUpdateRun, NodeUpdateProgress, runSettled } from "./NodeUpdateProgress";
import { reloadOnVersion } from "./update";
import { useCan } from "./role";
import { AddNodeDialog, InstallCommandModal } from "./AddNodeDialog";
import { NodeCard } from "./NodeCard";
import { AlertsDialog } from "./AlertsPanel";
import { ReachSettingsDialog } from "./ReachPanel";
import { agentSkew, cmpVersion, serverName } from "./NodeStatus";

export function NodesPanel() {
  const { t } = useTranslation();
  const canServers = useCan("servers.view");
  const canRouting = useCan("routing.view");
  const canManage = useCan("servers.manage");
  const canUpdate = useCan("system.update");
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
  // Geo categories feed the routing editor's domain/IP suggestions (same list for
  // the master and every node — one panel-side geosite/geoip).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [], iplist: [] });
  const [adding, setAdding] = useState(false);
  const [reachSettings, setReachSettings] = useState(false);
  const [alertsOpen, setAlertsOpen] = useState(false);
  // The check from other countries is off while its settings list no countries.
  const [reachOn, setReachOn] = useState(false);
  const [installCmd, setInstallCmd] = useState<string | null>(null);
  const [panelVersion, setPanelVersion] = useState("");
  // The newest release, and the one the master could update to when it is behind.
  const [latest, setLatest] = useState("");
  const [masterNext, setMasterNext] = useState("");
  const [updRun, setUpdRun] = useState<NodeUpdateRun | null>(null);
  const [updatingMaster, setUpdatingMaster] = useState(false);
  const { confirm, confirmNode } = useConfirm();

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
    if (canServers)
      getReachConfig()
        .then((c) => setReachOn(c.countries.length > 0))
        .catch(() => {});
    if (canUpdate)
      checkUpdate()
        .then((u) => {
          // A release still without its binaries is not offered to anyone yet.
          setLatest(u.ready && u.latest ? u.latest : "");
          setMasterNext(u.available && u.latest ? u.latest : "");
        })
        .catch(() => {});
    // The routing editor's suggestions — routing's to read.
    if (canRouting) getGeoCategories()
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
  const showingRestart =
    !!nodes?.some((n) => n.xray_restart) || (!!updRun && !!nodes && !runSettled(updRun, nodes));
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

  // The nodes an update reaches: enabled and connected at least once, as the server
  // picks them (RequestAllNodesUpdate).
  const updatable = nodes.filter((n) => !n.is_local && n.enabled && n.joined);
  // Behind the newest release — or, when it could not be read, behind the panel.
  const behind = updatable.filter((n) =>
    latest ? !!n.node_version && cmpVersion(n.node_version, latest) < 0 : agentSkew(n, panelVersion) === "older",
  );

  // updateNodes asks one node or all of them to update, then follows them in a dialog.
  const updateNodes = async (one?: NodeView) => {
    const v = latest ? { version: latest } : {};
    const ok = await confirm({
      title: one
        ? t(latest ? "nodes.upd.askOneTo" : "nodes.upd.askOne", { name: serverName(one), ...v })
        : t(latest ? "nodes.upd.askAllTo" : "nodes.upd.askAll", v),
      body: t("nodes.upd.body"),
      confirmLabel: t("general.update"),
    });
    if (!ok) return;
    // The dialog follows the nodes that have something to update; the rest are told
    // too, and answer that they are already current.
    const targets = one ? [one] : behind;
    try {
      if (one) await updateNodeVersion(one.id);
      else await updateAllNodes();
    } catch (e) {
      notifyError(errMessage(e));
      return;
    }
    setUpdRun({
      ids: targets.map((n) => n.id),
      target: latest,
      from: Object.fromEntries(targets.map((n) => [n.id, n.node_version])),
      started: Date.now(),
    });
    load();
  };

  const updateMaster = async () => {
    const ok = await confirm({
      title: t("general.updateTitle", { version: masterNext }),
      body: t("general.updateBody"),
      confirmLabel: t("general.update"),
    });
    if (!ok) return;
    setUpdatingMaster(true);
    try {
      await applyUpdate(false);
    } catch (e) {
      setUpdatingMaster(false);
      notifyError(errMessage(e));
      return;
    }
    reloadOnVersion(masterNext, (why) => {
      setUpdatingMaster(false);
      notifyError(t(why === "slow" ? "general.updateSlow" : "general.noAnswer"));
    });
  };

  return (
    <div className="flex flex-col gap-3.5">
      {confirmNode}
      <NodeUpdateProgress run={updRun} nodes={nodes} onClose={() => setUpdRun(null)} />
      <Modal open={updatingMaster} onClose={() => {}} dismissible={false} title={t("general.updateSection")}>
        <div className="flex items-start gap-3">
          <Spinner size={22} className="mt-0.5 shrink-0" />
          <p className="text-sm text-ink">{t("general.updatingHint")}</p>
        </div>
      </Modal>
      <div className="flex flex-wrap items-center gap-2">
        {canManage && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("nodes.addNode")}
          </Button>
        )}
        {canServers && (
          <Button size="sm" variant="outline" color="gray" onClick={() => setReachSettings(true)}>
            {t("reach.button")}
          </Button>
        )}
        {canServers && (
          <Button size="sm" variant="outline" color="gray" onClick={() => setAlertsOpen(true)}>
            {t("alerts.button")}
          </Button>
        )}
        {canUpdate && remoteCount > 0 && behind.length > 0 && (
          <Button size="sm" variant="outline" color="gray" onClick={() => updateNodes()}>
            {t("nodes.updateAll")}
          </Button>
        )}
        {canUpdate && masterNext && (
          <Button size="sm" variant="outline" color="gray" onClick={updateMaster} loading={updatingMaster}>
            {t("nodes.updateMaster", { version: masterNext })}{anyAhead ? " ⚠" : ""}
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
          reachOn={reachOn}
          servers={nodes.map((s) => ({
            id: s.id,
            name: s.name,
            bridges: s.bridges ?? [],
            // What can be an exit: in service, connected, with a connection to reach it by.
            available:
              s.enabled && (s.is_local || s.joined) && (s.bridges ?? []).some((b) => b.enabled),
          }))}
          onChanged={load}
          onUpdate={behind.includes(n) ? () => updateNodes(n) : undefined}
          onRegen={setInstallCmd}
        />
      ))}

      {/* One server is not a fleet: say what the page is for instead of leaving the
          master alone under a heading about servers. */}
      {remoteCount === 0 && (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-brand-600/10 bg-white px-4 py-10 text-center">
          <p className="text-sm font-semibold text-ink">{t("nodes.onlyThisServer")}</p>
          <p className="max-w-md text-xs text-ink-muted">{t("nodes.onlyThisServerHint")}</p>
          {canManage && (
            <Button size="sm" className="mt-2" onClick={() => setAdding(true)}>
              {t("nodes.addNode")}
            </Button>
          )}
        </div>
      )}

      {/* Other providers' servers are part of the servers section: servers.view to
          see them, servers.manage to change them. */}
      {canServers && (
        <ReadOnly when={!canManage}>
          <ExternalServers />
        </ReadOnly>
      )}

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
      {reachSettings && (
        <ReachSettingsDialog
          // What a monitor round checks: the master with an address, and every node
          // in service and connected.
          servers={
            nodes.filter((n) => (n.is_local ? !!n.host : n.enabled && n.joined && !!n.host)).length
          }
          onClose={() => setReachSettings(false)}
          onSaved={(c) => setReachOn(c.countries.length > 0)}
        />
      )}
      {alertsOpen && <AlertsDialog nodes={nodes} onClose={() => setAlertsOpen(false)} />}
      {installCmd && (
        <InstallCommandModal command={installCmd} onClose={() => setInstallCmd(null)} />
      )}
    </div>
  );
}
