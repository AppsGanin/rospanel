import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  applyConnections,
  resetConnections,
  getConnections,
  getGeoStatus,
  getRouting,
  saveRouting,
  setDecoy as saveDecoy,
  setGeoCadence as saveGeoCadence,
  setMasterName,
  setMasterPlacement,
  type Placement,
  setServerProxy,
  setXrayDNS,
  updateGeo,
  updateIPLists,
  setIPListCadence as saveIPListCadence,
  type GeoCategories,
  type GeoFile,
  type NodeView,
  type SystemProxy,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { ConnectionsEditor } from "./ConnectionsEditor";
import { InboundsEditor } from "./InboundsEditor";
import { ServerSnapshots } from "./ServerSnapshots";
import { canonicalDns, DnsEditor } from "./DnsEditor";
import { helperStatus } from "./egress";
import { decoyLabel } from "./GeneralSettings";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { TLSPanel } from "./TLSPanel";
import {
  EMPTY,
  GeoSection,
  hydrateRouting,
  IPListSection,
  RoutingEditor,
  type StatusBadge,
} from "./RoutingEditor";
import {
  ReadOnly, CenterLoader, Drawer, Section, Select, SettingRow, TextInput } from "./ui";
import { PlacementFields, placementOf } from "./PlacementFields";
import { DialogTabs, TabSaveBar, useServerRouting, useServerTabs } from "./ServerDialogParts";
import { useCan } from "./role";
import { SystemProxyEditor, systemProxyIssue } from "./SystemProxyEditor";

// MasterNameEditor lets the operator name the master server for config labels
// (shown as "<name> · VLESS…" in clients). Empty = no prefix.
// MasterSettingsDialog holds the master server's per-server settings. The master's
// protocols, decoy, routing and DNS are the panel's GLOBAL settings (edited in their
// own tabs), so here we only set its config-label name and point at the rest.
export function MasterSettingsDialog({
  node,
  decoys,
  geo,
  onClose,
  onRefresh,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onClose: () => void;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const { applying, apply } = useXrayApply();
  // The general tab (name + decoy) doesn't touch the Xray config, so it saves without the
  // xray-restart wait that `apply` blocks on — otherwise it hangs polling for a
  // restart that never comes.
  const [savingGeneral, setSavingGeneral] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [name, setName] = useState(node.master_label ?? "");
  const [decoy, setDecoy] = useState(node.decoy_template);
  const [pl, setPl] = useState<Placement>(placementOf(node));
  const [plBase, setPlBase] = useState<Placement>(placementOf(node));
  const plDirty = JSON.stringify(pl) !== JSON.stringify(plBase);
  const [genBase, setGenBase] = useState({
    name: node.master_label ?? "",
    decoy: node.decoy_template,
  });
  // The system proxy is part of this tab, so its draft rides the tab's single save.
  const [proxy, setProxy] = useState<SystemProxy>(node.proxy);
  const [proxyBase, setProxyBase] = useState<SystemProxy>(node.proxy);
  const proxyDirty = JSON.stringify(proxy) !== JSON.stringify(proxyBase);
  const proxyIssue = systemProxyIssue(proxy);
  const [dns, setDns] = useState(canonicalDns(node.xray_dns ?? ""));
  const [dnsBase, setDnsBase] = useState(canonicalDns(node.xray_dns ?? ""));
  // The server's own fields (servers.manage) and the system proxy (routing.manage)
  // save through different routes, so a role holding one saves only its part.
  const ownDirty = name !== genBase.name || decoy !== genBase.decoy || plDirty;
  const genDirty = ownDirty || proxyDirty;
  const dnsDirty = dns !== dnsBase;
  // Live egress status for the badges (master's egress runs locally, so the panel
  // knows the real state — unlike a node).
  const [warpRegistered, setWarpRegistered] = useState(node.warp_registered);
  const [operaRunning, setOperaRunning] = useState(false);
  const [operaAlive, setOperaAlive] = useState(false);
  const [proxyCounts, setProxyCounts] = useState<Record<string, number>>({});
  // Loopback entrances to the master's own egresses, shown so they can be pasted
  // elsewhere (the Telegram proxy, most obviously). Master only: a node's addresses
  // live on that node and mean nothing here.
  const [warpProxyURL, setWarpProxyURL] = useState("");
  const [operaProxyURL, setOperaProxyURL] = useState("");
  const [geoStatus, setGeoStatus] = useState<GeoFile[]>([]);
  const [ipListStatus, setIPListStatus] = useState<GeoFile[]>([]);
  const [geoCadence, setGeoCadence] = useState(0);
  const [ipListCadence, setIPListCadence] = useState(0);
  const [tab, setTab] = useState("general");
  // The tabs this role may see, and whether the one shown is read-only.
  const routingView = useCan("routing.view");
  const routingManage = useCan("routing.manage");
  const serversManage = useCan("servers.manage");
  const { visible, shown, readOnly } = useServerTabs(
    [
      { value: "general", label: t("settings.tabGeneral") },
      { value: "connections", label: t("nodes.tabConnections") },
      { value: "inbounds", label: t("nodes.tabInbounds") },
      { value: "routing", label: t("nodes.tabRouting") },
      { value: "dns", label: "DNS" },
      { value: "geo", label: "Geo" },
      { value: "iplist", label: t("nodes.tabLists") },
      { value: "domain", label: t("restore.domain") },
      { value: "snapshots", label: t("nodes.tabSnapshots") },
    ],
    tab,
  );
  const r = useServerRouting({
    cfg: EMPTY,
    warp: node.warp_enabled,
    opera: node.opera_enabled,
    country: node.opera_country,
  });
  const reset = r.reset;

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    getGeoStatus()
      .then((g) => {
        setGeoStatus(g.files);
        setIPListStatus(g.iplist_files ?? []);
        setGeoCadence(g.refresh_hours);
        setIPListCadence(g.iplist_refresh_hours ?? 0);
      })
      .catch(() => {});
    // A role without routing.view is not shown the routing tabs; the form is seeded
    // from the node list instead of asking for a 403.
    if (!routingView) {
      reset(
        hydrateRouting(node.routing),
        node.warp_enabled,
        node.opera_enabled,
        node.opera_country || "EU",
      );
      setLoaded(true);
      return;
    }
    getRouting()
      .then((info) => {
        reset(
          hydrateRouting(info.config),
          info.warp_enabled,
          info.opera_enabled,
          info.opera_country || "EU",
        );
        setWarpRegistered(info.warp_registered);
        setOperaRunning(info.opera_running);
        setOperaAlive(info.opera_alive);
        setProxyCounts(info.proxy_counts ?? {});
        setWarpProxyURL(info.warp_proxy_url ?? "");
        setOperaProxyURL(info.opera_proxy_url ?? "");
      })
      .catch((e) => {
        // If the live routing fetch fails, fall back to the config the node list
        // already carries (the master's own routing), so the tab shows the REAL rules
        // rather than an empty form a save would then persist over the real ones.
        reset(
          hydrateRouting(node.routing),
          node.warp_enabled,
          node.opera_enabled,
          node.opera_country || "EU",
        );
        notifyError(errMessage(e));
      })
      .finally(() => setLoaded(true));
  }, []);

  const refreshGeo = () =>
    apply(async () => {
      setGeoStatus((await updateGeo()).files);
      notifySuccess(t("nodes.geoUpdated"));
    });

  const refreshIPLists = () =>
    apply(async () => {
      setIPListStatus((await updateIPLists()).iplist_files ?? []);
      notifySuccess(t("nodes.iplistUpdated"));
    });

  // Mirrors changeGeoCadence: optimistic, rolled back on failure so the dropdown
  // never misreports the saved schedule.
  const changeIPListCadence = async (hours: number) => {
    const prev = ipListCadence;
    setIPListCadence(hours);
    try {
      await saveIPListCadence(hours);
      notifySuccess(t("nodes.iplistCadenceSaved"));
    } catch (e) {
      setIPListCadence(prev);
      notifyError(errMessage(e));
    }
  };

  const changeGeoCadence = async (hours: number) => {
    setGeoCadence(hours);
    try {
      await saveGeoCadence(hours);
      notifySuccess(t("nodes.geoCadenceSaved"));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // Same rule as the node dialog: the word follows what is saved, and an unsaved
  // switch says so.
  const warpBadge: StatusBadge =
    r.warpEnabled !== r.savedWarp
      ? { label: t("route.unsaved"), color: "orange" }
      : !r.savedWarp
        ? { label: t("conn.off"), color: "gray" }
        : warpRegistered
          ? { label: t("egress.alive"), color: "green" }
          : { label: t("nodes.notRegistered"), color: "orange" };
  const operaBadge: StatusBadge =
    r.operaEnabled !== r.savedOpera
      ? { label: t("route.unsaved"), color: "orange" }
      : (helperStatus(r.savedOpera, operaRunning, operaAlive, "") as StatusBadge);

  // Each tab saves on its own (like Connections/Geo/Domain) and stays open; onRefresh
  // updates the background list. These map to the panel's global settings behind the
  // master's card, so they stay as separate endpoints.
  const saveGeneral = async () => {
    setSavingGeneral(true);
    try {
      if (ownDirty) {
        await setMasterName(name.trim());
        if (plDirty) {
          await setMasterPlacement(pl);
          setPlBase(pl);
        }
        await saveDecoy(decoy);
      }
      // Only when it actually changed: the proxy write reconciles Xray, which a
      // rename has no business doing.
      if (proxyDirty) {
        await setServerProxy(0, proxy);
        setProxyBase(proxy);
      }
      setGenBase({ name, decoy });
      notifySuccess(t("nodes.generalSaved"));
      onRefresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSavingGeneral(false);
    }
  };

  const saveRoutingTab = () =>
    apply(async () => {
      // Routing + WARP/Opera together (one reconcile).
      await saveRouting(r.effective(), r.warpEnabled, r.operaEnabled, r.operaCountry);
      r.commit();
      notifySuccess(t("nodes.routingSaved"));
      onRefresh();
    });

  const saveDnsTab = () =>
    apply(async () => {
      await setXrayDNS(dns);
      setDnsBase(dns);
      notifySuccess(t("nodes.dnsSaved"));
      onRefresh();
    });

  return (
    <Drawer
      open
      onClose={onClose}
      wide
      title={t("nodes.masterSettings")}
      subtitle={node.host}
      toolbar={
        loaded ? (
          <DialogTabs
            value={shown}
            onChange={setTab}
            tabs={visible}
          />
        ) : undefined
      }
    >
      {!loaded ? (
        <CenterLoader />
      ) : (
        <ReadOnly when={readOnly}>

          {shown === "general" && (
            <div className="flex flex-col gap-3.5">
              <Section title={t("nodes.server")} flush>
                <SettingRow
                  label={t("groups.name")}
                  field={
                    <TextInput
                      value={name}
                      onChange={setName}
                      placeholder={t("nodes.masterNamePlaceholder")}
                    />
                  }
                />
                <SettingRow
                  label={t("nodes.decoy")}
                  field={
                    <Select
                      value={decoy}
                      onChange={setDecoy}
                      data={decoys.map((d) => ({ value: d, label: decoyLabel(d) }))}
                    />
                  }
                />
              </Section>
              <PlacementFields
                value={pl}
                onChange={setPl}
                online={node.online_users ?? 0}
                trafficUsed={node.traffic_period_used}
              />
              {/* The system proxy is routing's (POST /api/nodes/{id}/proxy): shown with
                  routing.view, editable with routing.manage. */}
              {routingView && (
              <ReadOnly when={!routingManage} own>
              <SystemProxyEditor
                host={node.host}
                value={proxy}
                saved={proxyBase}
                onChange={setProxy}
              />
              </ReadOnly>
              )}
              {/* Save answers to either permission: each part saves through its own route. */}
              <ReadOnly when={!serversManage && !routingManage} own>
              <TabSaveBar
                onSave={saveGeneral}
                onReset={() => {
                  setName(genBase.name);
                  setDecoy(genBase.decoy);
                  setPl(plBase);
                  setProxy(proxyBase);
                }}
                dirty={genDirty}
                busy={savingGeneral}
                invalid={proxyIssue !== ""}
              />
              </ReadOnly>
            </div>
          )}

          {shown === "connections" && (
            <ConnectionsEditor load={getConnections} save={applyConnections} reset={resetConnections} restartsPanel />
          )}

          {shown === "inbounds" && <InboundsEditor serverId={0} restartsPanel />}

          {shown === "routing" && (
            <div className="flex flex-col gap-3.5">
              <RoutingEditor
                cfg={r.cfg}
                onCfg={r.onCfg}
                laneSrc={r.laneSrc}
                setLaneSrc={r.setLaneSrc}
                warpEnabled={r.warpEnabled}
                setWarpEnabled={r.setWarpEnabled}
                warpBadge={warpBadge}
                operaEnabled={r.operaEnabled}
                setOperaEnabled={r.setOperaEnabled}
                operaCountry={r.operaCountry}
                setOperaCountry={r.setOperaCountry}
                operaBadge={operaBadge}
                warpProxyURL={warpProxyURL}
                operaProxyURL={operaProxyURL}
                proxyCounts={proxyCounts}
                geosite={geo.geosite}
                geoip={geo.geoip}
                iplist={geo.iplist}
                applying={applying}
              />
              <TabSaveBar
                onSave={saveRoutingTab}
                onReset={r.revert}
                dirty={r.dirty}
                busy={applying}
              />
            </div>
          )}

          {shown === "dns" && (
            <div className="flex flex-col gap-3.5">
              <Section title="DNS" desc={t("nodes.dnsMasterHint")} flush>
                <DnsEditor value={dns} onChange={setDns} />
              </Section>
              <TabSaveBar
                onSave={saveDnsTab}
                onReset={() => setDns(dnsBase)}
                dirty={dnsDirty}
                busy={applying}
              />
            </div>
          )}

          {shown === "geo" && (
            <GeoSection
              status={geoStatus}
              onRefresh={refreshGeo}
              refreshing={applying}
              cadence={geoCadence}
              onCadence={changeGeoCadence}
            />
          )}

          {shown === "iplist" && (
            <IPListSection
              status={ipListStatus}
              onRefresh={refreshIPLists}
              refreshing={applying}
              cadence={ipListCadence}
              onCadence={changeIPListCadence}
            />
          )}

          {/* Domain / TLS — its own load + change-domain button (page redirects
              on success), independent of this dialog's Save. */}
          {shown === "domain" && <TLSPanel />}

          {shown === "snapshots" && (
            <ServerSnapshots
              onRolledBack={() => {
                onRefresh();
                onClose();
              }}
            />
          )}
        </ReadOnly>
      )}
      <ApplyingModal open={applying} />
    </Drawer>
  );
}
