import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  FINGERPRINTS,
  type ConnectionsStatus,
  type ConnectionsUpdate,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useAction } from "./hooks";
import { NameVarsHint } from "./namevars";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  CenterLoader,
  cn,
  Code,
  IconChevron,
  Mono,
  Section,
  Select,
  SettingRow,
  Switch,
  TagsInput,
  TextInput,
  useConfirm,
} from "./ui";

// Field is a read-only fact about a protocol: what it is, not what to set.
function Field({ label, value }: { label: string; value: string }) {
  return (
    <SettingRow
      label={label}
      control={<Mono className="text-[11px] text-ink-muted">{value}</Mono>}
    />
  );
}

// LongField is the same for a value no row edge can hold — a key, a set of short
// IDs, a path: the label on its line and the value in a block under it.
function LongField({ label, value }: { label: string; value: string }) {
  return (
    <SettingRow label={label}>
      <Code block copy>
        {value}
      </Code>
    </SettingRow>
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

// Hy carries the Hysteria2 lane's editable shape. obfs is DISPLAY only — the key is
// never typed, only minted by the server (see auth.RandomObfsKey), so obfsAction is
// what the operator actually changes: keep it, mint a new one, or switch it off.
type ObfsAction = "keep" | "regen" | "off";
type Hy = {
  port: number;
  start: number;
  end: number;
  interval: string;
  obfs: string;
  obfsAction: ObfsAction;
};

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
// awgParamSummary is the obfuscation, in the order the client config lists it and
// omitting what is not set. It used to be a fixed nine-field string, which after
// the move to 3.1 quietly showed a server's parameters as if they were still the
// old set: no header key, no imitation, no padding beyond S1/S2.
function awgParamSummary(p: ConnectionsStatus["awg_params"]): string {
  const parts = [
    `Jc=${p.jc}`,
    `Jmin=${p.jmin}`,
    `Jmax=${p.jmax}`,
    `S1=${p.s1}`,
    `S2=${p.s2}`,
    p.s3 ? `S3=${p.s3}` : "",
    p.s4 ? `S4=${p.s4}` : "",
    `H1=${p.h1}`,
    `H2=${p.h2}`,
    `H3=${p.h3}`,
    `H4=${p.h4}`,
    // Every entry carries a value. A bare word next to "S1=53" reads as a
    // parameter that exists and is empty — which is what the first version of
    // this line did with the imitation chains, and it was read exactly that way.
    // So: the profile is named rather than dumped as hex, and the header key
    // shows enough of itself to be visibly a key.
    p.imitation ? `Imit=${p.imitation}` : p.i1 ? "Imit=on" : "",
    p.header_key ? `HPK=${p.header_key.slice(0, 8)}…` : "",
    p.padding ? `Pad=${p.padding}` : "",
    p.trailers ? "Trailers=on" : "",
    p.rekey_after ? `Rekey=${p.rekey_after}` : "",
    p.reject_after ? `Reject=${p.reject_after}` : "",
    p.keepalive ? `Keepalive=${p.keepalive}` : "",
  ]
  return parts.filter(Boolean).join(" ")
}

export function ConnectionsEditor({
  load,
  save,
  reset,
  restartsPanel,
}: {
  load: () => Promise<ConnectionsStatus>;
  save: (u: ConnectionsUpdate) => Promise<ConnectionsStatus>;
  // Factory reset of this server's connections (ports, protocols, donor, anti-DPI,
  // custom inbounds). Absent ⇒ the button is not offered.
  reset?: () => Promise<ConnectionsStatus>;
  restartsPanel: boolean;
}) {
  const { t } = useTranslation();
  const { confirm, confirmNode } = useConfirm();
  const [status, setStatus] = useState<ConnectionsStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { busy, run } = useAction();
  const { applying, apply: applyXray } = useXrayApply();

  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [fps, setFps] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [hy, setHy] = useState<Hy>({ port: 0, start: 0, end: 0, interval: "5-10", obfs: "", obfsAction: "keep" });
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
    hy: { port: 0, start: 0, end: 0, interval: "5-10", obfs: "", obfsAction: "keep" },
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
    const h: Hy = {
      port: s.hysteria_port, start: s.hop_start, end: s.hop_end,
      interval: s.hop_interval || "5-10", obfs: s.hysteria_obfs || "", obfsAction: "keep",
    };
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

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load()
      .then(applyStatus)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
  }, []);

  const protocolsChanged = Object.keys(enabled).some((k) => enabled[k] !== saved.enabled[k]);
  const portsChanged = hy.port !== saved.hy.port || hy.start !== saved.hy.start || hy.end !== saved.hy.end;
  // The obfuscation key is part of the SERVER config (Xray's finalmask block), not
  // just of the links — changing it has to restart Xray, or the panel hands out
  // links for a key the listener is not using yet.
  const obfsChanged = hy.obfsAction !== "keep";
  const hyChanged = portsChanged || hy.interval !== saved.hy.interval || obfsChanged;
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
  const restartsXray =
    protocolsChanged || portsChanged || obfsChanged || realityChanged || regenReality || antiServerChanged;

  const setHyNum = (key: "port" | "start" | "end") => (v: string) =>
    setHy((h) => ({ ...h, [key]: Number(v.replace(/\D/g, "")) || 0 }));

  // A reset takes the same road as a save — validated, reconciled, audited — and on
  // the master restarts Xray like any port change would.
  const doReset = async () => {
    if (!reset) return;
    const ok = await confirm({
      title: t("conn.resetTitle"),
      body: t("conn.resetBody"),
      confirmLabel: t("conn.resetConfirm"),
      danger: true,
    });
    if (!ok) return;
    const run1 = async () => {
      applyStatus(await reset());
      notifySuccess(t("conn.resetDone"));
    };
    if (restartsPanel) applyXray(run1);
    else run(run1);
  };

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
        // "off" clears it, "keep" round-trips what the server already has, and
        // regen_obfs makes the server mint one and ignore this value entirely.
        hysteria_obfs: hy.obfsAction === "off" ? "" : hy.obfs,
        regen_obfs: hy.obfsAction === "regen",
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
    <div className="flex flex-col gap-3.5">
      {status.protocols.map((p) => {
        const isOpen = !!open[p.key];
        const on = !!enabled[p.key];
        return (
          <Section
            key={p.key}
            title={
              // The whole name is the disclosure control; the switch beside it is
              // not, so turning a protocol on does not also unfold its form.
              <button
                type="button"
                onClick={() => setOpen((o) => ({ ...o, [p.key]: !o[p.key] }))}
                className="flex min-w-0 items-center gap-2 text-left"
              >
                <IconChevron
                  className={cn(
                    "shrink-0 text-gray-400 transition-transform",
                    isOpen && "rotate-180",
                  )}
                />
                <span className="truncate">{p.name}</span>
                <Mono className="shrink-0 text-[11px] font-normal text-ink-muted">
                  {p.port}
                </Mono>
                {!on && (
                  <span className="shrink-0 text-[11px] font-normal text-ink-muted">
                    {t("conn.off")}
                  </span>
                )}
              </button>
            }
            action={
              <Switch
                checked={on}
                onChange={(v) => setEnabled((e) => ({ ...e, [p.key]: v }))}
              />
            }
            flush
          >
            {isOpen && (
              <>
                <SettingRow
                  label={t("conn.name")}
                  hint={t("conn.nameHint", { name: p.name })}
                >
                  <div className="flex flex-col gap-1.5">
                    <TextInput
                      value={names[p.key] ?? ""}
                      onChange={(v) => setNames((n) => ({ ...n, [p.key]: v }))}
                      placeholder={p.name}
                    />
                    <NameVarsHint
                      onInsert={(v) =>
                        setNames((n) => ({
                          ...n,
                          [p.key]: ((n[p.key] ?? "") + " " + v).trim(),
                        }))
                      }
                    />
                  </div>
                </SettingRow>

                <Field label={t("conn.transport")} value={p.transport} />
                <Field label={t("conn.security")} value={p.security} />
                {p.note && <Field label={t("conn.note")} value={p.note} />}

                {p.fingerprint && (
                  <SettingRow
                    label="Fingerprint (uTLS)"
                    hint={t("conn.fpHint")}
                    field={
                      <Select
                        data={FP_OPTIONS}
                        value={fps[p.key] ?? "firefox"}
                        onChange={(v) => setFps((f) => ({ ...f, [p.key]: v }))}
                      />
                    }
                  />
                )}

                {p.key === "hysteria2" &&
                  (on ? (
                    <>
                      {/* Port and the hop range read as one setting, so they share
                          a row and keep their own captions. */}
                      <SettingRow>
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
                      </SettingRow>
                      <SettingRow
                        label={t("conn.hopInterval")}
                        hint={t("conn.hopHint")}
                        field={
                          <Select
                            data={hopIntervals()}
                            value={hy.interval}
                            onChange={(v) => setHy((h) => ({ ...h, interval: v }))}
                          />
                        }
                      />
                      {/* Salamander. Shown rather than hidden: both ends need the same
                          value and it is already inside every link the panel hands out.
                          Read only, like the REALITY material — the key is minted by the
                          server, never invented by whoever is filling in the form. */}
                      <SettingRow
                        label={t("conn.obfs")}
                        hint={t("conn.obfsHint")}
                        control={
                          <span className="flex items-center gap-2">
                            <Button
                              variant="subtle"
                              size="xs"
                              color={hy.obfsAction === "regen" ? "orange" : "gray"}
                              onClick={() =>
                                setHy((h) => ({
                                  ...h,
                                  obfsAction: h.obfsAction === "regen" ? "keep" : "regen",
                                }))
                              }
                            >
                              {t("conn.obfsGenerate")}
                            </Button>
                            {(hy.obfs !== "" || hy.obfsAction === "off") && (
                              <Button
                                variant="subtle"
                                size="xs"
                                color={hy.obfsAction === "off" ? "orange" : "gray"}
                                onClick={() =>
                                  setHy((h) => ({
                                    ...h,
                                    obfsAction: h.obfsAction === "off" ? "keep" : "off",
                                  }))
                                }
                              >
                                {t("conn.obfsDisable")}
                              </Button>
                            )}
                          </span>
                        }
                      >
                        <Code block copy>
                          {hy.obfsAction === "off"
                            ? t("conn.obfsOff")
                            : hy.obfsAction === "regen"
                              ? t("conn.obfsWillRegen")
                              : hy.obfs || t("conn.obfsOff")}
                        </Code>
                      </SettingRow>
                    </>
                  ) : (
                    <SettingRow hint={t("conn.enableHysteria")} />
                  ))}

                {p.key === "reality" &&
                  (on ? (
                    <>
                      <SettingRow
                        label={t("conn.port")}
                        field={
                          <TextInput
                            type="number"
                            value={String(reality.port)}
                            onChange={(v) =>
                              setReality((r) => ({
                                ...r,
                                port: Number(v.replace(/\D/g, "")) || 0,
                              }))
                            }
                          />
                        }
                      />
                      <SettingRow label={t("conn.masquerade")}>
                        <TagsInput
                          value={reality.dests}
                          onChange={(v) => setReality((r) => ({ ...r, dests: v }))}
                          placeholder={t("conn.sniPlaceholder")}
                        />
                      </SettingRow>
                      <SettingRow
                        label={t("conn.antiReplay")}
                        hint={t("conn.antiReplayHint")}
                        control={
                          <Switch
                            checked={reality.antiReplay}
                            onChange={(v) => setReality((r) => ({ ...r, antiReplay: v }))}
                          />
                        }
                      />
                      <LongField label="Public key" value={status.reality_public_key} />
                      <LongField label="Short IDs" value={status.reality_short_id} />
                      <LongField label={t("conn.xhttpPath")} value={status.reality_path} />
                      <SettingRow
                        hint={t("conn.realityHint")}
                        control={
                          <Button
                            size="xs"
                            variant="light"
                            color={regenReality ? "orange" : "gray"}
                            onClick={() => setRegenReality((v) => !v)}
                          >
                            {t(regenReality ? "conn.keysWillRegen" : "conn.regenKeys")}
                          </Button>
                        }
                      />
                    </>
                  ) : (
                    <SettingRow hint={t("conn.enableReality")} />
                  ))}

                {p.key === "awg" &&
                  (on ? (
                    <>
                      <SettingRow
                        label={t("conn.awgPort")}
                        field={
                          <TextInput
                            type="number"
                            value={awgCfg.port ? String(awgCfg.port) : ""}
                            placeholder={t("conn.awgPortAuto")}
                            onChange={(v) =>
                              setAwgCfg((g) => ({
                                ...g,
                                port: Number(v.replace(/\D/g, "")) || 0,
                              }))
                            }
                          />
                        }
                      />
                      <SettingRow
                        label={t("conn.awgDns")}
                        field={
                          <TextInput
                            value={awgCfg.dns}
                            placeholder={t("conn.awgDnsAuto")}
                            onChange={(v) => setAwgCfg((g) => ({ ...g, dns: v }))}
                          />
                        }
                      />
                      {status.awg_public_key && (
                        <>
                          <LongField label="Public key" value={status.awg_public_key} />
                          <LongField
                            label={t("conn.awgParams")}
                            value={awgParamSummary(status.awg_params)}
                          />
                        </>
                      )}
                      {status.awg_error && (
                        <SettingRow
                          hint={<span className="text-warning">{status.awg_error}</span>}
                        />
                      )}
                      <SettingRow
                        hint={t("conn.awgHint")}
                        control={
                          <Button
                            size="xs"
                            variant="light"
                            color={regenAwg ? "orange" : "gray"}
                            onClick={() => setRegenAwg((v) => !v)}
                          >
                            {t(regenAwg ? "conn.keysWillRegen" : "conn.regenKeys")}
                          </Button>
                        }
                      />
                    </>
                  ) : (
                    <SettingRow hint={t("conn.enableAwg")} />
                  ))}
              </>
            )}
          </Section>
        );
      })}

      <Section title={t("conn.antiDpi")} desc={t("conn.antiDpiHint")} flush>
        <SettingRow
          label={t("conn.fragment")}
          hint={`${t("conn.fragmentHint")} (VLESS-Vision).`}
          control={
            <Switch
              checked={anti.fragment}
              onChange={(v) => setAnti((a) => ({ ...a, fragment: v }))}
            />
          }
        />
        <SettingRow
          label={t("conn.blockQuic")}
          hint={t("conn.blockQuicHint")}
          control={
            <Switch
              checked={anti.blockQuic}
              onChange={(v) => setAnti((a) => ({ ...a, blockQuic: v }))}
            />
          }
        />
        <SettingRow
          label={t("conn.requireTls13")}
          hint={t("conn.requireTls13Hint")}
          control={
            <Switch
              checked={anti.min13}
              onChange={(v) => setAnti((a) => ({ ...a, min13: v }))}
            />
          }
        />
      </Section>

      {reset && (
        <Section
          title={t("conn.resetTitle")}
          action={
            <Button
              size="xs"
              variant="light"
              color="red"
              className="whitespace-nowrap"
              onClick={doReset}
              disabled={busy || applying}
            >
              {t("conn.reset")}
            </Button>
          }
          desc={t("conn.resetHint")}
          flush
        />
      )}

      {confirmNode}
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
