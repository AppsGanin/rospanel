import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  FINGERPRINTS,
  type ConnectionsStatus,
  type ConnectionsUpdate,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useAction } from "./hooks";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  IconChevron,
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
type Awg = { port: number; dns: string };

// ConnectionsEditor is the full connection editor (protocols on/off + names +
// fingerprints + ports + hop + WS + REALITY donor/keys/regen/port/anti-replay +
// anti-DPI) for one server. It's controlled: the caller injects how to load and save
// (master = global connections; a node = its own), so the same UI drives both. It has
// no SaveBar (it lives in a modal tab); an inline bar appears when dirty.
//
// restartsPanel: when true (the master), a config-restarting save shows the panel's
// "restarting" modal and waits for it to come back. For a node the panel doesn't
// restart — the node applies the pushed config itself — so it's a plain save.
export function ConnectionsEditor({
  load,
  save,
  restartsPanel,
}: {
  load: () => Promise<ConnectionsStatus>;
  save: (u: ConnectionsUpdate) => Promise<ConnectionsStatus>;
  restartsPanel: boolean;
}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<ConnectionsStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { busy, run } = useAction();
  const { applying, apply: applyXray } = useXrayApply();

  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [fps, setFps] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [hy, setHy] = useState<Hy>({ port: 0, start: 0, end: 0, interval: "5-10" });
  const [reality, setReality] = useState<Reality>({ port: 0, dests: [], antiReplay: false });
  const [anti, setAnti] = useState<Anti>({ fragment: false, min13: false, blockQuic: false });
  const [regenReality, setRegenReality] = useState(false);
  const [awgCfg, setAwgCfg] = useState<Awg>({ port: 0, dns: "" });
  const [regenAwg, setRegenAwg] = useState(false);
  const [saved, setSaved] = useState<{
    enabled: Record<string, boolean>;
    fps: Record<string, string>;
    names: Record<string, string>;
    hy: Hy;
    reality: Reality;
    anti: Anti;
    awg: Awg;
  }>({
    enabled: {},
    fps: {},
    names: {},
    hy: { port: 0, start: 0, end: 0, interval: "5-10" },
    reality: { port: 0, dests: [], antiReplay: false },
    anti: { fragment: false, min13: false, blockQuic: false },
    awg: { port: 0, dns: "" },
  });
  const [open, setOpen] = useState<Record<string, boolean>>({});

  const applyStatus = (s: ConnectionsStatus) => {
    setStatus(s);
    const en: Record<string, boolean> = {};
    const fp: Record<string, string> = {};
    const nm: Record<string, string> = {};
    s.protocols.forEach((p) => {
      en[p.key] = p.enabled;
      if (p.fingerprint) fp[p.key] = p.fingerprint;
      nm[p.key] = p.display_name || "";
    });
    const h: Hy = { port: s.hysteria_port, start: s.hop_start, end: s.hop_end, interval: s.hop_interval || "5-10" };
    const r: Reality = {
      port: s.reality_port,
      dests: s.reality_dest ? s.reality_dest.split(",").map((d) => d.trim()).filter(Boolean) : [],
      antiReplay: s.reality_anti_replay,
    };
    const a: Anti = { fragment: s.tls_fragment, min13: s.tls_min13, blockQuic: s.block_quic };
    const g: Awg = { port: s.awg_port, dns: s.awg_dns || "" };
    setEnabled(en);
    setFps(fp);
    setNames(nm);
    setHy(h);
    setReality(r);
    setAnti(a);
    setAwgCfg(g);
    setRegenReality(false);
    setRegenAwg(false);
    setSaved({ enabled: en, fps: fp, names: nm, hy: h, reality: r, anti: a, awg: g });
  };

  useEffect(() => {
    load()
      .then(applyStatus)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const protocolsChanged = Object.keys(enabled).some((k) => enabled[k] !== saved.enabled[k]);
  const portsChanged = hy.port !== saved.hy.port || hy.start !== saved.hy.start || hy.end !== saved.hy.end;
  const hyChanged = portsChanged || hy.interval !== saved.hy.interval;
  const realityChanged =
    reality.port !== saved.reality.port ||
    reality.dests.join(",") !== saved.reality.dests.join(",") ||
    reality.antiReplay !== saved.reality.antiReplay;
  const fpsChanged = Object.keys(fps).some((k) => fps[k] !== saved.fps[k]);
  const namesChanged = Object.keys(names).some((k) => names[k] !== saved.names[k]);
  const antiServerChanged = anti.min13 !== saved.anti.min13;
  const antiClientChanged = anti.fragment !== saved.anti.fragment || anti.blockQuic !== saved.anti.blockQuic;
  const awgChanged = awgCfg.port !== saved.awg.port || awgCfg.dns !== saved.awg.dns || regenAwg;
  const dirty =
    fpsChanged || namesChanged || protocolsChanged || hyChanged ||
    realityChanged || regenReality || antiServerChanged || antiClientChanged || awgChanged;
  // Config-affecting changes restart Xray (on the master) or re-push to the node.
  const restartsXray = protocolsChanged || portsChanged || realityChanged || regenReality || antiServerChanged;

  const setHyNum = (key: "port" | "start" | "end") => (v: string) =>
    setHy((h) => ({ ...h, [key]: Number(v.replace(/\D/g, "")) || 0 }));

  const doSave = () => {
    const run1 = async () => {
      const s = await save({
        protocols: enabled,
        fingerprints: fps,
        names,
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
        awg_port: awgCfg.port,
        awg_dns: awgCfg.dns,
        regen_awg_keys: regenAwg,
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
    setHy(saved.hy);
    setReality(saved.reality);
    setAnti(saved.anti);
    setAwgCfg(saved.awg);
    setRegenReality(false);
    setRegenAwg(false);
  };

  if (!loaded) return <CenterLoader />;
  if (!status) return null;

  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-1 gap-3">
        {status.protocols.map((p) => {
          const isOpen = !!open[p.key];
          const on = !!enabled[p.key];
          return (
            <div
              key={p.key}
              className="overflow-hidden rounded-xl border border-gray-200/80 bg-gray-50/60"
            >
              <button
                type="button"
                onClick={() => setOpen((o) => ({ ...o, [p.key]: !o[p.key] }))}
                className="flex w-full items-center justify-between gap-2 p-4 text-left"
              >
                <div className="flex min-w-0 items-center gap-2">
                  <IconChevron
                    className={`shrink-0 text-gray-400 transition-transform ${isOpen ? "rotate-180" : ""}`}
                  />
                  <span className="font-medium text-ink">{p.name}</span>
                  <Badge color="gray">{p.port}</Badge>
                  {!on && <Badge color="gray">{t("conn.off")}</Badge>}
                </div>
                <span onClick={(e) => e.stopPropagation()} className="flex items-center">
                  <Switch checked={on} onChange={(v) => setEnabled((e) => ({ ...e, [p.key]: v }))} />
                </span>
              </button>

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
                      <p className="mt-2 text-xs text-ink-muted">
                        {t("conn.fpHint")}
                      </p>
                    </div>
                  )}

                  {p.key === "hysteria2" &&
                    (on ? (
                      <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                        <div className="grid grid-cols-3 gap-2">
                          <TextInput label={t("conn.port")} type="number" value={String(hy.port)} onChange={setHyNum("port")} />
                          <TextInput label={t("conn.hopFrom")} type="number" value={String(hy.start)} onChange={setHyNum("start")} />
                          <TextInput label={t("conn.hopTo")} type="number" value={String(hy.end)} onChange={setHyNum("end")} />
                        </div>
                        <Select
                          label={t("conn.hopInterval")}
                          data={hopIntervals()}
                          value={hy.interval}
                          onChange={(v) => setHy((h) => ({ ...h, interval: v }))}
                        />
                        <p className="text-xs text-ink-muted">
                          {t("conn.hopHint")}
                        </p>
                      </div>
                    ) : (
                      <p className="border-t border-gray-100 pt-3 text-xs text-ink-muted">
                        {t("conn.enableHysteria")}
                      </p>
                    ))}

                  {p.key === "reality" &&
                    (on ? (
                      <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                        <TextInput
                          label={t("conn.port")}
                          type="number"
                          value={String(reality.port)}
                          onChange={(v) => setReality((r) => ({ ...r, port: Number(v.replace(/\D/g, "")) || 0 }))}
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
                        <p className="text-xs text-ink-muted">
                          {t("conn.realityHint")}
                        </p>
                      </div>
                    ) : (
                      <p className="border-t border-gray-100 pt-3 text-xs text-ink-muted">
                        {t("conn.enableReality")}
                      </p>
                    ))}

                  {p.key === "awg" &&
                    (on ? (
                      <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                          <TextInput
                            label={t("conn.awgPort")}
                            type="number"
                            value={awgCfg.port ? String(awgCfg.port) : ""}
                            placeholder={t("conn.awgPortAuto")}
                            onChange={(v) => setAwgCfg((g) => ({ ...g, port: Number(v.replace(/\D/g, "")) || 0 }))}
                          />
                          <TextInput
                            label={t("conn.awgDns")}
                            value={awgCfg.dns}
                            placeholder={t("conn.awgDnsAuto")}
                            onChange={(v) => setAwgCfg((g) => ({ ...g, dns: v }))}
                          />
                        </div>
                        {status.awg_public_key && (
                          <>
                            <LongField label="Public key" value={status.awg_public_key} />
                            <LongField
                              label={t("conn.awgParams")}
                              value={`Jc=${status.awg_params.jc} Jmin=${status.awg_params.jmin} Jmax=${status.awg_params.jmax} S1=${status.awg_params.s1} S2=${status.awg_params.s2} H1=${status.awg_params.h1} H2=${status.awg_params.h2} H3=${status.awg_params.h3} H4=${status.awg_params.h4}`}
                            />
                          </>
                        )}
                        {status.awg_error && (
                          <p className="warning-tint rounded-lg px-2.5 py-1.5 text-xs text-warning">{status.awg_error}</p>
                        )}
                        <div>
                          <Button
                            size="sm"
                            variant="light"
                            color={regenAwg ? "orange" : "gray"}
                            onClick={() => setRegenAwg((v) => !v)}
                          >
                            {t(regenAwg ? "conn.keysWillRegen" : "conn.regenKeys")}
                          </Button>
                        </div>
                        <p className="text-xs text-ink-muted">{t("conn.awgHint")}</p>
                      </div>
                    ) : (
                      <p className="border-t border-gray-100 pt-3 text-xs text-ink-muted">
                        {t("conn.enableAwg")}
                      </p>
                    ))}
                </div>
              )}
            </div>
          );
        })}
      </div>

      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <h3 className="mb-1 font-bold text-ink">{t("conn.antiDpi")}</h3>
        <p className="mb-3 text-sm text-ink-muted">
          {t("conn.antiDpiHint")}
        </p>
        <div className="flex flex-col divide-y divide-gray-100">
          <label className="flex items-center justify-between gap-3 py-3 first:pt-0">
            <span className="text-sm">
              {t("conn.fragment")}
              <span className="block text-xs text-ink-muted">
                {t("conn.fragmentHint")}
                (VLESS-Vision).
              </span>
            </span>
            <Switch checked={anti.fragment} onChange={(v) => setAnti((a) => ({ ...a, fragment: v }))} />
          </label>
          <label className="flex items-center justify-between gap-3 py-3">
            <span className="text-sm">
              {t("conn.blockQuic")}
              <span className="block text-xs text-ink-muted">
                {t("conn.blockQuicHint")}
              </span>
            </span>
            <Switch checked={anti.blockQuic} onChange={(v) => setAnti((a) => ({ ...a, blockQuic: v }))} />
          </label>
          <label className="flex items-center justify-between gap-3 py-3 last:pb-0">
            <span className="text-sm">
              {t("conn.requireTls13")}
              <span className="block text-xs text-ink-muted">
                {t("conn.requireTls13Hint")}
              </span>
            </span>
            <Switch checked={anti.min13} onChange={(v) => setAnti((a) => ({ ...a, min13: v }))} />
          </label>
        </div>
      </div>

      <div className="flex flex-col gap-2 border-t border-gray-100 pt-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-xs text-ink-muted">
          {t("conn.saveHint")}
        </p>
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
      <ApplyingModal open={applying} />
    </div>
  );
}
