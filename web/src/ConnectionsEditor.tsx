import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  FINGERPRINTS,
  createInbound,
  deleteInbound,
  getInboundCatalog,
  listInbounds,
  regenInboundReality,
  updateInbound,
  getNodeReservedPorts,
  type ConnectionsStatus,
  type ConnectionsUpdate,
  type Inbound,
  type InboundCatalog,
  type InboundInput,
  type PortInfo,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useAction } from "./hooks";
import i18n from "./i18n";
import {
  blank,
  InboundForm,
  InboundRow,
  toInput,
} from "./InboundsEditor";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  IconChevron,
  Modal,
  Select,
  Switch,
  TagsInput,
  TextInput,
} from "./ui";

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-sm text-ink-muted">{label}</span>
      <span className="text-right text-sm font-medium">{value}</span>
    </div>
  );
}

// LongField stacks the label over a wrapping monospace value — for long read-only
// values (keys, shortIds) that would overflow a single row on mobile.
function LongField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-sm text-ink-muted">{label}</span>
      <code className="block break-all rounded border border-gray-200 bg-white/60 px-2 py-1 font-mono text-xs text-ink">
        {value}
      </code>
    </div>
  );
}

const FP_OPTIONS = FINGERPRINTS.map((f) => ({
  value: f,
  label: f.charAt(0).toUpperCase() + f.slice(1),
}));

const hopIntervals = () => [
  { value: "5-10", label: i18n.t("conn.sec", { range: "5–10" }) },
  { value: "10-30", label: i18n.t("conn.sec", { range: "10–30" }) },
  { value: "30-60", label: i18n.t("conn.sec", { range: "30–60" }) },
  { value: "60-120", label: i18n.t("conn.sec", { range: "60–120" }) },
];

type Hy = { port: number; start: number; end: number; interval: string };
type Reality = { port: number; dests: string[]; antiReplay: boolean };
type Anti = { fragment: boolean; min13: boolean; blockQuic: boolean };

// ConnectionsEditor is the unified connection editor (built-in protocols + custom inbounds +
// Anti-DPI + factory reset) for one server. Controlled: the caller injects how to load,
// save and reset (master = global connections; a node = its own).
export function ConnectionsEditor({
  load,
  save,
  reset,
  serverId = 0,
  restartsPanel,
}: {
  load: () => Promise<ConnectionsStatus>;
  save: (u: ConnectionsUpdate) => Promise<ConnectionsStatus>;
  reset?: () => Promise<ConnectionsStatus>;
  serverId?: number;
  restartsPanel: boolean;
}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<ConnectionsStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { busy, run } = useAction();
  const { applying, apply: applyXray } = useXrayApply();

  // Standard connections state
  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [deleted, setDeleted] = useState<Record<string, boolean>>({});
  const [fps, setFps] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [vlessPort, setVlessPort] = useState<number>(443);
  const [hy, setHy] = useState<Hy>({ port: 443, start: 443, end: 443, interval: "5-10" });
  const [reality, setReality] = useState<Reality>({ port: 8443, dests: [], antiReplay: false });
  const [anti, setAnti] = useState<Anti>({ fragment: false, min13: false, blockQuic: false });
  const [regenReality, setRegenReality] = useState(false);
  const [saved, setSaved] = useState<{
    enabled: Record<string, boolean>;
    fps: Record<string, string>;
    names: Record<string, string>;
    vlessPort: number;
    hy: Hy;
    reality: Reality;
    anti: Anti;
  }>({
    enabled: {},
    fps: {},
    names: {},
    vlessPort: 443,
    hy: { port: 443, start: 443, end: 443, interval: "5-10" },
    reality: { port: 8443, dests: [], antiReplay: false },
    anti: { fragment: false, min13: false, blockQuic: false },
  });
  const [open, setOpen] = useState<Record<string, boolean>>({});

  // Custom inbounds state
  const [inbounds, setInbounds] = useState<Inbound[] | null>(null);
  const [catalog, setCatalog] = useState<InboundCatalog | null>(null);
  const [reservedPorts, setReservedPorts] = useState<PortInfo[]>([]);
  const [editingInbound, setEditingInbound] = useState<{ id: number; v: InboundInput } | null>(null);
  const [confirmDelInbound, setConfirmDelInbound] = useState<Inbound | null>(null);

  const [confirmReset, setConfirmReset] = useState(false);

  const reloadInbounds = () => {
    listInbounds(serverId)
      .then(setInbounds)
      .catch((e) => {
        notifyError(errMessage(e));
        setInbounds([]);
      });
    if (serverId > 0) {
      getNodeReservedPorts(serverId)
        .then((r) => setReservedPorts(r.ports || []))
        .catch(() => {});
    }
  };

  const applyStatus = (s: ConnectionsStatus, explicitDeleted?: Record<string, boolean>) => {
    setStatus(s);
    const en: Record<string, boolean> = {};
    const fp: Record<string, string> = {};
    const nm: Record<string, string> = {};
    const vlessP = Number(s.protocols.find((p) => p.key === "vless")?.port) || 443;
    setVlessPort(vlessP);
    s.protocols.forEach((p) => {
      en[p.key] = p.enabled;
      if (p.fingerprint) fp[p.key] = p.fingerprint;
      nm[p.key] = p.display_name || "";
    });
    const h: Hy = {
      port: s.hysteria_port,
      start: s.hop_start,
      end: s.hop_end,
      interval: s.hop_interval || "5-10",
    };
    const r: Reality = {
      port: s.reality_port,
      dests: s.reality_dest
        ? s.reality_dest
            .split(",")
            .map((d) => d.trim())
            .filter(Boolean)
        : [],
      antiReplay: s.reality_anti_replay,
    };
    const a: Anti = { fragment: s.tls_fragment, min13: s.tls_min13, blockQuic: s.block_quic };
    setEnabled(en);
    setFps(fp);
    setNames(nm);
    setHy(h);
    setReality(r);
    setAnti(a);
    setRegenReality(false);
    if (explicitDeleted !== undefined) {
      setDeleted(explicitDeleted);
    }
    setSaved({ enabled: en, fps: fp, names: nm, vlessPort: vlessP, hy: h, reality: r, anti: a });
  };

  useEffect(() => {
    const promises: [Promise<ConnectionsStatus>, Promise<Inbound[]>, Promise<InboundCatalog>] = [
      load(),
      listInbounds(serverId),
      getInboundCatalog(),
    ];
    Promise.all(promises)
      .then(([s, inbList, cat]) => {
        applyStatus(s);
        setInbounds(inbList);
        setCatalog(cat);
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));

    if (serverId > 0) {
      getNodeReservedPorts(serverId)
        .then((r) => setReservedPorts(r.ports || []))
        .catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverId]);

  const protocolsChanged = Object.keys(enabled).some((k) => enabled[k] !== saved.enabled[k]);
  const portsChanged =
    vlessPort !== saved.vlessPort ||
    hy.port !== saved.hy.port ||
    hy.start !== saved.hy.start ||
    hy.end !== saved.hy.end;
  const hyChanged = portsChanged || hy.interval !== saved.hy.interval;
  const realityChanged =
    reality.port !== saved.reality.port ||
    reality.dests.join(",") !== saved.reality.dests.join(",") ||
    reality.antiReplay !== saved.reality.antiReplay;
  const fpsChanged = Object.keys(fps).some((k) => fps[k] !== saved.fps[k]);
  const namesChanged = Object.keys(names).some((k) => names[k] !== saved.names[k]);
  const antiServerChanged = anti.min13 !== saved.anti.min13;
  const antiClientChanged =
    anti.fragment !== saved.anti.fragment || anti.blockQuic !== saved.anti.blockQuic;
  const dirty =
    fpsChanged ||
    namesChanged ||
    protocolsChanged ||
    portsChanged ||
    hyChanged ||
    realityChanged ||
    regenReality ||
    antiServerChanged ||
    antiClientChanged;

  // Config-affecting changes restart Xray (on the master) or re-push to the node.
  const restartsXray =
    protocolsChanged || portsChanged || realityChanged || regenReality || antiServerChanged;

  const setHyNum = (key: "port" | "start" | "end") => (v: string) =>
    setHy((h) => ({ ...h, [key]: Number(v.replace(/\D/g, "")) || 0 }));

  const doSave = () => {
    const run1 = async () => {
      const s = await save({
        protocols: enabled,
        fingerprints: fps,
        names,
        vless_port: vlessPort,
        hysteria_port: hy.port,
        hop_start: hy.start,
        hop_end: hy.end,
        hop_interval: hy.interval,
        reality_port: reality.port,
        reality_dest: reality.dests.join(","),
        reality_anti_replay: reality.antiReplay,
        regen_reality_keys: regenReality,
        tls_fragment: anti.fragment,
        tls_min13: anti.min13,
        block_quic: anti.blockQuic,
      });
      applyStatus(s);
      notifySuccess(t("common.saved"));
    };
    if (restartsPanel && restartsXray) applyXray(run1);
    else run(run1);
  };

  const cancel = () => {
    setEnabled(saved.enabled);
    setFps(saved.fps);
    setNames(saved.names);
    setVlessPort(saved.vlessPort);
    setHy(saved.hy);
    setReality(saved.reality);
    setAnti(saved.anti);
    setRegenReality(false);
  };

  // Custom inbound mutations
  const writeInbound = (fn: () => Promise<unknown>) => {
    const task = async () => {
      await fn();
      await reloadInbounds();
      notifySuccess(t("common.saved"));
    };
    if (restartsPanel) applyXray(task);
    else run(task);
  };

  const saveCustomInbound = () => {
    if (!editingInbound) return;
    const v = editingInbound.v;
    writeInbound(async () => {
      if (editingInbound.id === 0) await createInbound(serverId, v);
      else await updateInbound(editingInbound.id, v);
      setEditingInbound(null);
    });
  };

  const toggleInbound = (v: Inbound, isEn: boolean) =>
    writeInbound(() => updateInbound(v.id, { ...toInput(v), enabled: isEn }));

  const removeInbound = (v: Inbound) =>
    writeInbound(async () => {
      await deleteInbound(v.id);
      setConfirmDelInbound(null);
    });

  const regenInbound = (v: Inbound) => writeInbound(() => regenInboundReality(v.id));

  // Factory reset action
  const doFactoryReset = () => {
    if (!reset) return;
    const task = async () => {
      const s = await reset();
      setDeleted({});
      applyStatus(s, {});
      await reloadInbounds();
      setConfirmReset(false);
      notifySuccess(t("conn.resetFactoryDone"));
    };
    if (restartsPanel) applyXray(task);
    else run(task);
  };

  if (!loaded || !status || inbounds === null || !catalog) return <CenterLoader />;

  const customLimitReached = inbounds.length >= catalog.max;
  const visibleProtocols = status.protocols.filter((p) => {
    if (deleted[p.key]) return false;
    if (p.key === "reality" && !p.enabled && enabled[p.key] !== true) return false;
    if (serverId > 0 && !p.enabled && enabled[p.key] !== true) return false;
    return true;
  });
  const totalConnections = visibleProtocols.length + inbounds.length;

  return (
    <div className="flex flex-col gap-5">
      {/* Top action toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 pb-3">
        <div>
          <h3 className="font-bold text-ink">{t("nodes.tabConnections")}</h3>
          <p className="text-xs text-ink-muted">{t("conn.saveHint")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {reset && (
            <Button
              size="sm"
              variant="light"
              color="orange"
              onClick={() => setConfirmReset(true)}
              disabled={busy || applying}
            >
              {t("conn.resetFactory")}
            </Button>
          )}
          <Button
            size="sm"
            onClick={() => setEditingInbound({ id: 0, v: blank() })}
            disabled={busy || applying || customLimitReached}
          >
            + {t("conn.addConnection")}
          </Button>
        </div>
      </div>

      {/* ── Reserved Ports Summary (if any) ─────────────────────────────────── */}
      {reservedPorts.length > 0 && (
        <div className="rounded-xl border border-indigo-200 bg-indigo-50/70 p-3.5 text-xs dark:border-indigo-900/50 dark:bg-indigo-950/30">
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <span className="font-bold text-indigo-950 dark:text-indigo-100">
              🛡️ {t("nodes.reservedPorts")} ({reservedPorts.length})
            </span>
            <span className="text-[11px] text-ink-muted">{t("nodes.reservedPortsHint")}</span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {reservedPorts.map((pi) => (
              <span
                key={`${pi.port}-${pi.protocol}`}
                className={cn(
                  "inline-flex items-center gap-1 rounded-md px-2 py-0.5 font-mono text-[11px]",
                  pi.is_owner
                    ? "bg-amber-100/80 text-amber-900 dark:bg-amber-950/60 dark:text-amber-200"
                    : "bg-indigo-100/80 text-indigo-900 dark:bg-indigo-950/60 dark:text-indigo-200",
                )}
                title={pi.is_owner ? t("nodes.portOwner") : t("nodes.portTenant")}
              >
                <strong>{pi.port}</strong>/{pi.protocol.toUpperCase()} ({pi.service || (pi.is_owner ? t("nodes.portOwner") : t("nodes.portTenant"))})
              </span>
            ))}
          </div>
        </div>
      )}

      {/* ── Unified Connections List ─────────────────────────────────────────── */}
      <div className="flex flex-col gap-3">
        {totalConnections === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-200 p-6 text-center">
            <p className="text-sm text-ink-muted">{t("inb.empty")}</p>
            <div className="mt-3 flex justify-center gap-2">
              {reset && (
                <Button size="sm" variant="light" color="orange" onClick={() => setConfirmReset(true)}>
                  {t("conn.resetFactory")}
                </Button>
              )}
              <Button size="sm" onClick={() => setEditingInbound({ id: 0, v: blank() })}>
                + {t("conn.addConnection")}
              </Button>
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3">
            {/* Built-in connections */}
            {visibleProtocols.map((p) => {
              const isOpen = !!open[p.key];
              const on = !!enabled[p.key];
              const displayName = names[p.key] || p.name;
              const portDisplay =
                p.key === "vless" ? vlessPort : p.key === "reality" ? reality.port : hy.port;

              return (
                <div
                  key={p.key}
                  className="overflow-hidden rounded-xl border border-gray-200/80 bg-gray-50/60"
                >
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => setOpen((o) => ({ ...o, [p.key]: !o[p.key] }))}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        setOpen((o) => ({ ...o, [p.key]: !o[p.key] }));
                      }
                    }}
                    className="flex w-full cursor-pointer items-center justify-between gap-2 p-4 text-left"
                  >
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <IconChevron
                        className={`shrink-0 text-gray-400 transition-transform ${isOpen ? "rotate-180" : ""}`}
                      />
                      <span className="font-medium text-ink">{displayName}</span>
                      <Badge color="gray">{portDisplay}</Badge>
                      <Badge color="gray">{p.transport}</Badge>
                      <Badge color="gray">{p.security}</Badge>
                      {!on && <Badge color="gray">{t("conn.off")}</Badge>}
                    </div>
                    <div className="flex items-center gap-3" onClick={(e) => e.stopPropagation()}>
                      <Switch
                        checked={on}
                        onChange={(v) => setEnabled((e) => ({ ...e, [p.key]: v }))}
                        disabled={busy || applying}
                      />
                    </div>
                  </div>

                  {isOpen && (
                    <div className="flex flex-col gap-3 border-t border-gray-100 px-4 pb-4 pt-3">
                      <div className="flex flex-col gap-2">
                        <TextInput
                          label={t("conn.name")}
                          value={names[p.key] ?? ""}
                          onChange={(v) => setNames((n) => ({ ...n, [p.key]: v }))}
                          placeholder={p.name}
                        />
                        <p className="text-xs text-ink-muted">
                          {t("conn.nameHint", { name: p.name })}
                        </p>
                      </div>

                      <div className="flex flex-col gap-1 border-t border-gray-100 pt-3">
                        <Field label={t("conn.transport")} value={p.transport} />
                        <Field label={t("conn.security")} value={p.security} />
                        {p.note && <Field label={t("conn.note")} value={p.note} />}
                      </div>

                      {p.fingerprint && (
                        <div className="border-t border-gray-100 pt-3">
                          <Select
                            label="Fingerprint (uTLS)"
                            data={FP_OPTIONS}
                            value={fps[p.key] ?? "firefox"}
                            onChange={(v) => setFps((f) => ({ ...f, [p.key]: v }))}
                          />
                          <p className="mt-2 text-xs text-ink-muted">{t("conn.fpHint")}</p>
                        </div>
                      )}

                      {p.key === "hysteria2" && (
                        <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                          <div className="grid grid-cols-3 gap-2">
                            <TextInput
                              label={t("conn.port")}
                              type="number"
                              value={String(hy.port)}
                              onChange={setHyNum("port")}
                            />
                            <TextInput
                              label={t("conn.hopFrom")}
                              type="number"
                              value={String(hy.start)}
                              onChange={setHyNum("start")}
                            />
                            <TextInput
                              label={t("conn.hopTo")}
                              type="number"
                              value={String(hy.end)}
                              onChange={setHyNum("end")}
                            />
                          </div>
                          <Select
                            label={t("conn.hopInterval")}
                            data={hopIntervals()}
                            value={hy.interval}
                            onChange={(v) => setHy((h) => ({ ...h, interval: v }))}
                          />
                          <p className="text-xs text-ink-muted">{t("conn.hopHint")}</p>
                        </div>
                      )}

                      {p.key === "reality" && (
                        <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                          <TextInput
                            label={t("conn.port")}
                            type="number"
                            value={String(reality.port)}
                            onChange={(v) =>
                              setReality((r) => ({
                                ...r,
                                port: Number(v.replace(/\D/g, "")) || 0,
                              }))
                            }
                          />
                          <TagsInput
                            label={t("conn.masquerade")}
                            value={reality.dests}
                            onChange={(v) => setReality((r) => ({ ...r, dests: v }))}
                            placeholder={t("conn.sniPlaceholder")}
                          />
                          <label className="flex items-center justify-between gap-3">
                            <span className="text-sm">
                              {t("conn.antiReplay")}
                              <span className="block text-xs text-ink-muted">
                                {t("conn.antiReplayHint")}
                              </span>
                            </span>
                            <Switch
                              checked={reality.antiReplay}
                              onChange={(v) => setReality((r) => ({ ...r, antiReplay: v }))}
                            />
                          </label>
                          <LongField label="Public key" value={status.reality_public_key} />
                          <LongField label="Short IDs" value={status.reality_short_id} />
                          <LongField label={t("conn.xhttpPath")} value={status.reality_path} />
                          <div>
                            <Button
                              size="sm"
                              variant="light"
                              color={regenReality ? "orange" : "gray"}
                              onClick={() => setRegenReality((v) => !v)}
                            >
                              {t(regenReality ? "conn.keysWillRegen" : "conn.regenKeys")}
                            </Button>
                          </div>
                          <p className="text-xs text-ink-muted">{t("conn.realityHint")}</p>
                        </div>
                      )}

                    </div>
                  )}
                </div>
              );
            })}

            {/* Custom inbounds */}
            {inbounds.map((v) => (
              <InboundRow
                key={v.id}
                v={v}
                busy={busy || applying}
                onToggle={(en) => toggleInbound(v, en)}
                onEdit={() => setEditingInbound({ id: v.id, v: toInput(v) })}
                onDelete={() => setConfirmDelInbound(v)}
                onRegen={() => regenInbound(v)}
              />
            ))}
          </div>
        )}
      </div>

      {/* Save bar for dirty standard connection parameters */}
      <div className="flex flex-col gap-2 border-t border-gray-100 pt-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-xs text-ink-muted">{t("conn.saveHint")}</p>
        <div className="flex justify-end gap-2">
          <Button
            variant="light"
            color="gray"
            onClick={cancel}
            disabled={!dirty || busy || applying}
          >
            {t("common.cancel")}
          </Button>
          <Button onClick={doSave} loading={busy || applying} disabled={!dirty}>
            {t("common.save")}
          </Button>
        </div>
      </div>

      {/* ── Section: Anti-DPI Transport Hardening ────────────────────────────── */}
      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <h3 className="mb-1 font-bold text-ink">{t("conn.antiDpi")}</h3>
        <p className="mb-3 text-sm text-ink-muted">{t("conn.antiDpiHint")}</p>
        <div className="flex flex-col divide-y divide-gray-100">
          <label className="flex items-center justify-between gap-3 py-3 first:pt-0">
            <span className="text-sm">
              {t("conn.fragment")}
              <span className="block text-xs text-ink-muted">
                {t("conn.fragmentHint")} (VLESS-Vision).
              </span>
            </span>
            <Switch
              checked={anti.fragment}
              onChange={(v) => setAnti((a) => ({ ...a, fragment: v }))}
            />
          </label>
          <label className="flex items-center justify-between gap-3 py-3">
            <span className="text-sm">
              {t("conn.blockQuic")}
              <span className="block text-xs text-ink-muted">
                {t("conn.blockQuicHint")}
              </span>
            </span>
            <Switch
              checked={anti.blockQuic}
              onChange={(v) => setAnti((a) => ({ ...a, blockQuic: v }))}
            />
          </label>
          <label className="flex items-center justify-between gap-3 py-3 last:pb-0">
            <span className="text-sm">
              {t("conn.requireTls13")}
              <span className="block text-xs text-ink-muted">
                {t("conn.requireTls13Hint")}
              </span>
            </span>
            <Switch
              checked={anti.min13}
              onChange={(v) => setAnti((a) => ({ ...a, min13: v }))}
            />
          </label>
        </div>
      </div>

      {/* ── Modals ───────────────────────────────────────────────────────────── */}

      {/* Custom Inbound create / edit modal */}
      <Modal
        open={!!editingInbound}
        onClose={() => setEditingInbound(null)}
        title={t(editingInbound?.id ? "inb.connection" : "inb.newConnection")}
        size="lg"
      >
        {editingInbound && (
          <InboundForm
            v={editingInbound.v}
            catalog={catalog}
            onChange={(v) => setEditingInbound({ ...editingInbound, v })}
            onCancel={() => setEditingInbound(null)}
            onSave={saveCustomInbound}
            busy={busy || applying}
          />
        )}
      </Modal>

      {/* Custom Inbound delete confirmation modal */}
      <Modal
        open={!!confirmDelInbound}
        onClose={() => setConfirmDelInbound(null)}
        title={t("inb.deleteTitle")}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            {t("inb.deleteBody", { name: confirmDelInbound?.name ?? "" })}
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setConfirmDelInbound(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              color="red"
              loading={busy || applying}
              onClick={() => confirmDelInbound && removeInbound(confirmDelInbound)}
            >
              {t("common.delete")}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Factory reset confirmation modal */}
      <Modal
        open={confirmReset}
        onClose={() => setConfirmReset(false)}
        title={t("conn.resetFactoryTitle")}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">{t("conn.resetFactoryDesc")}</p>
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setConfirmReset(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              color="red"
              loading={busy || applying}
              onClick={doFactoryReset}
            >
              {t("conn.resetFactory")}
            </Button>
          </div>
        </div>
      </Modal>

      <ApplyingModal open={applying} />
    </div>
  );
}
