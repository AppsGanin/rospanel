import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  applyNodeConnections,
  resetNodeConnections,
  getNodeConnections,
  getNodeTLS,
  setNodeACME,
  type Placement,
  setNodeDNS,
  setServerProxy,
  setNodeRouting,
  updateNode,
  type GeoCategories,
  type NodeView,
  type SystemProxy,
} from "./api";
import { ConnectionsEditor } from "./ConnectionsEditor";
import { InboundsEditor } from "./InboundsEditor";
import { canonicalDns, DnsEditor } from "./DnsEditor";
import { decoyLabel } from "./GeneralSettings";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { TLSPanel } from "./TLSPanel";
import { hydrateRouting, RoutingEditor, type StatusBadge } from "./RoutingEditor";
import { Drawer, Section, Select, SettingRow, TextInput } from "./ui";
import { PlacementFields, placementOf } from "./PlacementFields";
import {
  DialogTabs,
  NodeGeoCard,
  TabSaveBar,
  clampCoefficient,
  nodeDefaultRouting,
  useServerRouting,
} from "./ServerDialogParts";
import { SystemProxyEditor, systemProxyIssue } from "./SystemProxyEditor";

// NodeSettingsDialog edits a remote node's full per-server config: name, decoy,
// protocol overrides, its OWN routing + egress (the same editor as the master), and
// its DNS. Routing/egress and DNS each either inherit the panel's or are the node's
// own override. Egress (proxy lanes / WARP / Opera) is independent of the master and
// only meaningful with own routing, so it lives inside the routing editor.
export function NodeSettingsDialog({
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
  const [name, setName] = useState(node.name);
  const [decoy, setDecoy] = useState(node.decoy_template);
  // The per-node quota multiplier (1 = neutral). Kept as a string so the field can be
  // cleared while typing; parsed on save.
  const [coef, setCoef] = useState(String(node.traffic_coefficient || 1));
  const [pl, setPl] = useState<Placement>(placementOf(node));
  const [plBase, setPlBase] = useState<Placement>(placementOf(node));
  const plDirty = JSON.stringify(pl) !== JSON.stringify(plBase);
  // genBase / dnsBase are the last-saved snapshots powering dirty-tracking + revert on
  // the General and DNS tabs (routing carries its own inside useServerRouting).
  const [genBase, setGenBase] = useState({
    name: node.name,
    decoy: node.decoy_template,
    coef: String(node.traffic_coefficient || 1),
  });
  // The system proxy is part of the General tab, so its draft lives here and rides
  // that tab's single save.
  const [proxy, setProxy] = useState<SystemProxy>(node.proxy);
  const [proxyBase, setProxyBase] = useState<SystemProxy>(node.proxy);
  const proxyDirty = JSON.stringify(proxy) !== JSON.stringify(proxyBase);
  const proxyIssue = systemProxyIssue(proxy);
  const r = useServerRouting({
    cfg: node.routing ? hydrateRouting(node.routing) : nodeDefaultRouting(),
    warp: node.warp_enabled,
    opera: node.opera_enabled,
    country: node.opera_country,
  });
  const [dns, setDns] = useState(canonicalDns(node.xray_dns ?? ""));
  const [dnsBase, setDnsBase] = useState(canonicalDns(node.xray_dns ?? ""));
  const [saving, setSaving] = useState(false);
  const [tab, setTab] = useState("general");
  const genDirty =
    name !== genBase.name || decoy !== genBase.decoy || coef !== genBase.coef || proxyDirty || plDirty;
  const dnsDirty = dns !== dnsBase;

  // Status words describe the SAVED egress: WARP registration is known from the
  // node's report, Opera runs remotely so the panel only knows on/off. A flipped but
  // unsaved switch says so rather than claiming the lane is already up.
  const warpBadge: StatusBadge =
    r.warpEnabled !== r.savedWarp
      ? { label: t("route.unsaved"), color: "orange" }
      : !r.savedWarp
        ? { label: t("conn.off"), color: "gray" }
        : node.warp_registered
          ? { label: t("egress.alive"), color: "green" }
          : { label: t("nodes.willRegister"), color: "orange" };
  const operaBadge: StatusBadge =
    r.operaEnabled !== r.savedOpera
      ? { label: t("route.unsaved"), color: "orange" }
      : r.savedOpera
        ? { label: t("nodes.on"), color: "green" }
        : { label: t("conn.off"), color: "gray" };

  // Each tab saves on its own (like Connections/Geo/Domain) and stays open; onRefresh
  // updates the background list. General persists name/decoy, Routing the routing +
  // egress, DNS its own endpoint — three independent saves.
  const saveGeneral = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await updateNode(node.id, {
        name: name.trim(),
        host: node.host, // domain is changed from the Domain tab
        decoy_template: decoy,
        traffic_coefficient: clampCoefficient(coef),
        placement: pl,
        // Protocols are edited on the Connections tab; omitting them here tells the
        // panel to preserve the current values (never revert a just-made change).
      });
      // Only when it actually changed: the proxy write reconciles the server's Xray,
      // which is not something a rename should trigger.
      if (proxyDirty) {
        await setServerProxy(node.id, proxy);
        setProxyBase(proxy);
      }
      setGenBase({ name, decoy, coef });
      setPlBase(pl);
      notifySuccess(t("nodes.generalSaved"));
      onRefresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveRouting = async () => {
    setSaving(true);
    try {
      // Routing + egress — always the node's OWN (no inherit toggle). An empty routing
      // config just means "mostly direct". DNS is saved separately.
      await setNodeRouting(
        node.id,
        r.effective(),
        r.warpEnabled,
        r.operaEnabled,
        r.operaCountry,
      );
      r.commit();
      notifySuccess(t("nodes.routingSaved"));
      onRefresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveDns = async () => {
    setSaving(true);
    try {
      // Empty ⇒ inherit the panel's default resolver.
      await setNodeDNS(node.id, dns.trim() ? dns : null);
      setDnsBase(dns);
      notifySuccess(t("nodes.dnsSaved"));
      onRefresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      open
      onClose={onClose}
      wide
      title={t("nodes.settingsOf", { name: node.name })}
      subtitle={node.host}
      toolbar={
        <DialogTabs
          value={tab}
          onChange={setTab}
          tabs={[
            { value: "general", label: t("settings.tabGeneral") },
            { value: "connections", label: t("nodes.tabConnections") },
            { value: "inbounds", label: t("nodes.tabInbounds") },
            { value: "routing", label: t("nodes.tabRouting") },
            { value: "dns", label: "DNS" },
            { value: "geo", label: "Geo" },
            { value: "domain", label: t("restore.domain") },
          ]}
        />
      }
    >

      {tab === "general" && (
        <div className="flex flex-col gap-3.5">
          <Section title={t("nodes.server")} flush>
            <SettingRow
              label={t("groups.name")}
              field={
                <TextInput
                  value={name}
                  onChange={setName}
                  placeholder={t("nodes.namePlaceholder")}
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
            <SettingRow
              label={t("nodes.coefficient")}
              hint={t("nodes.coefficientHint")}
              field={
                <TextInput
                  type="number"
                  value={coef}
                  onChange={setCoef}
                  placeholder="1.0"
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
          <SystemProxyEditor
            host={node.host}
            value={proxy}
            saved={proxyBase}
            onChange={setProxy}
          />
          <TabSaveBar
            onSave={saveGeneral}
            onReset={() => {
              setName(genBase.name);
              setDecoy(genBase.decoy);
              setCoef(genBase.coef);
              setPl(plBase);
              setProxy(proxyBase);
            }}
            dirty={genDirty}
            busy={saving}
            invalid={proxyIssue !== ""}
          />
        </div>
      )}

      {tab === "connections" && (
        <ConnectionsEditor
          load={() => getNodeConnections(node.id)}
          save={(u) => applyNodeConnections(node.id, u)}
          reset={() => resetNodeConnections(node.id)}
          restartsPanel={false}
        />
      )}

      {tab === "inbounds" && <InboundsEditor serverId={node.id} restartsPanel={false} />}

      {tab === "routing" && (
        <div className="flex flex-col gap-3.5">
          {/* Routing + egress — always the node's own (independent of the master). */}
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
            proxyCounts={{}}
            geosite={geo.geosite}
            geoip={geo.geoip}
            iplist={geo.iplist}
            applying={saving}
            liveStatus={false}
          />
          <TabSaveBar onSave={saveRouting} onReset={r.revert} dirty={r.dirty} busy={saving} />
        </div>
      )}

      {tab === "dns" && (
        <div className="flex flex-col gap-3.5">
          <Section title="DNS" desc={t("nodes.dnsNodeHint")} flush>
            <DnsEditor value={dns} onChange={setDns} />
          </Section>
          <TabSaveBar
            onSave={saveDns}
            onReset={() => setDns(dnsBase)}
            dirty={dnsDirty}
            busy={saving}
          />
        </div>
      )}

      {tab === "geo" && <NodeGeoCard node={node} onChanged={onRefresh} />}

      {tab === "domain" && (
        <TLSPanel
          load={() => getNodeTLS(node.id)}
          save={(t, e, p) => setNodeACME(node.id, t, e, p)}
          redirectOnSuccess={false}
          onChanged={onRefresh}
        />
      )}
    </Drawer>
  );
}
