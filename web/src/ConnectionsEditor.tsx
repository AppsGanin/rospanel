import { useEffect, useState } from "react";
import {
  FINGERPRINTS,
  type ConnectionsStatus,
  type ConnectionsUpdate,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useAction } from "./hooks";
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

const HOP_INTERVALS = [
  { value: "5-10", label: "5–10 с" },
  { value: "10-30", label: "10–30 с" },
  { value: "30-60", label: "30–60 с" },
  { value: "60-120", label: "60–120 с" },
];

type Hy = { port: number; start: number; end: number; interval: string };
type Reality = { port: number; dests: string[]; antiReplay: boolean };
type Anti = { fragment: boolean; min13: boolean; blockQuic: boolean };

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
  const [status, setStatus] = useState<ConnectionsStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { busy, run } = useAction();
  const { applying, apply: applyXray } = useXrayApply();

  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [fps, setFps] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [hy, setHy] = useState<Hy>({ port: 0, start: 0, end: 0, interval: "5-10" });
  const [wsPath, setWsPath] = useState("");
  const [reality, setReality] = useState<Reality>({ port: 0, dests: [], antiReplay: false });
  const [anti, setAnti] = useState<Anti>({ fragment: false, min13: false, blockQuic: false });
  const [regenReality, setRegenReality] = useState(false);
  const [saved, setSaved] = useState<{
    enabled: Record<string, boolean>;
    fps: Record<string, string>;
    names: Record<string, string>;
    hy: Hy;
    wsPath: string;
    reality: Reality;
    anti: Anti;
  }>({
    enabled: {},
    fps: {},
    names: {},
    hy: { port: 0, start: 0, end: 0, interval: "5-10" },
    wsPath: "",
    reality: { port: 0, dests: [], antiReplay: false },
    anti: { fragment: false, min13: false, blockQuic: false },
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
    const ws = s.ws_path.replace(/^\/+/, "");
    setEnabled(en);
    setFps(fp);
    setNames(nm);
    setHy(h);
    setWsPath(ws);
    setReality(r);
    setAnti(a);
    setRegenReality(false);
    setSaved({ enabled: en, fps: fp, names: nm, hy: h, wsPath: ws, reality: r, anti: a });
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
  const wsChanged = wsPath !== saved.wsPath;
  const realityChanged =
    reality.port !== saved.reality.port ||
    reality.dests.join(",") !== saved.reality.dests.join(",") ||
    reality.antiReplay !== saved.reality.antiReplay;
  const fpsChanged = Object.keys(fps).some((k) => fps[k] !== saved.fps[k]);
  const namesChanged = Object.keys(names).some((k) => names[k] !== saved.names[k]);
  const antiServerChanged = anti.min13 !== saved.anti.min13;
  const antiClientChanged = anti.fragment !== saved.anti.fragment || anti.blockQuic !== saved.anti.blockQuic;
  const dirty =
    fpsChanged || namesChanged || protocolsChanged || hyChanged || wsChanged ||
    realityChanged || regenReality || antiServerChanged || antiClientChanged;
  // Config-affecting changes restart Xray (on the master) or re-push to the node.
  const restartsXray = protocolsChanged || portsChanged || wsChanged || realityChanged || regenReality || antiServerChanged;

  const setHyNum = (key: "port" | "start" | "end") => (v: string) =>
    setHy((h) => ({ ...h, [key]: Number(v.replace(/\D/g, "")) || 0 }));

  const doSave = () => {
    const run1 = async () => {
      const s = await save({
        protocols: enabled,
        fingerprints: fps,
        names,
        ws_path: wsPath,
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
      notifySuccess("Сохранено");
    };
    if (restartsPanel && restartsXray) applyXray(run1);
    else run(run1);
  };

  const cancel = () => {
    setEnabled(saved.enabled);
    setFps(saved.fps);
    setNames(saved.names);
    setHy(saved.hy);
    setWsPath(saved.wsPath);
    setReality(saved.reality);
    setAnti(saved.anti);
    setRegenReality(false);
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
                  {!on && <Badge color="gray">выключен</Badge>}
                </div>
                <span onClick={(e) => e.stopPropagation()} className="flex items-center">
                  <Switch checked={on} onChange={(v) => setEnabled((e) => ({ ...e, [p.key]: v }))} />
                </span>
              </button>

              {isOpen && (
                <div className="flex flex-col gap-3 border-t border-gray-100 px-4 pb-4 pt-3">
                  <div className="flex flex-col gap-2">
                    <TextInput
                      label="Название подключения"
                      value={names[p.key] ?? ""}
                      onChange={(v) => setNames((n) => ({ ...n, [p.key]: v }))}
                      placeholder={p.name}
                    />
                    <p className="text-xs text-ink-muted">
                      Имя узла, которое видит клиент в списке подключений. Пусто —
                      используется «{p.name}».
                    </p>
                  </div>

                  <div className="flex flex-col gap-1 border-t border-gray-100 pt-3">
                    <Field label="Транспорт" value={p.transport} />
                    <Field label="Шифрование" value={p.security} />
                    {p.note && <Field label="Примечание" value={p.note} />}
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
                        Отпечаток TLS-клиента, имитируемый ссылкой (параметр fp).
                      </p>
                    </div>
                  )}

                  {p.key === "trojan" && (
                    <div className="flex flex-col gap-2 border-t border-gray-100 pt-3">
                      <TextInput
                        label="Путь WebSocket"
                        value={wsPath}
                        onChange={(v) => setWsPath(v.replace(/^\/+/, ""))}
                        placeholder="path"
                      />
                      <p className="text-xs text-ink-muted">
                        Путь WS-туннеля Trojan. Слеш в начале добавляется
                        автоматически — вводи без него.
                      </p>
                    </div>
                  )}

                  {p.key === "hysteria2" &&
                    (on ? (
                      <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                        <div className="grid grid-cols-3 gap-2">
                          <TextInput label="Порт" type="number" value={String(hy.port)} onChange={setHyNum("port")} />
                          <TextInput label="Хоп от" type="number" value={String(hy.start)} onChange={setHyNum("start")} />
                          <TextInput label="Хоп до" type="number" value={String(hy.end)} onChange={setHyNum("end")} />
                        </div>
                        <Select
                          label="Интервал смены порта"
                          data={HOP_INTERVALS}
                          value={hy.interval}
                          onChange={(v) => setHy((h) => ({ ...h, interval: v }))}
                        />
                        <p className="text-xs text-ink-muted">
                          Клиент разбрасывает трафик по диапазону «хоп от–до»,
                          nftables сводит его на базовый порт.
                        </p>
                      </div>
                    ) : (
                      <p className="border-t border-gray-100 pt-3 text-xs text-ink-muted">
                        Включите HYSTERIA-UDP, чтобы настроить порты и интервал.
                      </p>
                    ))}

                  {p.key === "reality" &&
                    (on ? (
                      <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
                        <TextInput
                          label="Порт"
                          type="number"
                          value={String(reality.port)}
                          onChange={(v) => setReality((r) => ({ ...r, port: Number(v.replace(/\D/g, "")) || 0 }))}
                        />
                        <TagsInput
                          label="Маскировка (SNI)"
                          value={reality.dests}
                          onChange={(v) => setReality((r) => ({ ...r, dests: v }))}
                          placeholder="max.ru — добавить и Enter…"
                        />
                        <label className="flex items-center justify-between gap-3">
                          <span className="text-sm">
                            Анти-replay
                            <span className="block text-xs text-ink-muted">
                              Окно ±60 с против повтора рукопожатия зондом. Может
                              резать клиентов со сбитыми часами.
                            </span>
                          </span>
                          <Switch
                            checked={reality.antiReplay}
                            onChange={(v) => setReality((r) => ({ ...r, antiReplay: v }))}
                          />
                        </label>
                        <LongField label="Public key" value={status.reality_public_key} />
                        <LongField label="Short IDs" value={status.reality_short_id} />
                        <LongField label="gRPC service" value={status.reality_service_name} />
                        <div>
                          <Button
                            size="sm"
                            variant="light"
                            color={regenReality ? "orange" : "gray"}
                            onClick={() => setRegenReality((v) => !v)}
                          >
                            {regenReality ? "Ключи будут перегенерированы ✓" : "Перегенерировать ключи"}
                          </Button>
                        </div>
                        <p className="text-xs text-ink-muted">
                          REALITY заимствует TLS реального сайта (TLS 1.3 + H2,
                          проверяется при сохранении). Можно указать несколько SNI —
                          первый основной (идёт в ссылки), сервер принимает все;
                          альтернативные должны делить сертификат основного донора
                          (быть его SAN).
                        </p>
                      </div>
                    ) : (
                      <p className="border-t border-gray-100 pt-3 text-xs text-ink-muted">
                        Включите VLESS-GRPC-REALITY, чтобы настроить порт и маскировку.
                      </p>
                    ))}
                </div>
              )}
            </div>
          );
        })}
      </div>

      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <h3 className="mb-1 font-bold text-ink">Анти-DPI</h3>
        <p className="mb-3 text-sm text-ink-muted">
          Меры против обнаружения и блокировки (ТСПУ). Фрагментация и блок QUIC
          меняют только выдаваемые клиентам конфиги; TLS 1.3 — серверный.
        </p>
        <div className="flex flex-col divide-y divide-gray-100">
          <label className="flex items-center justify-between gap-3 py-3 first:pt-0">
            <span className="text-sm">
              Фрагментация ClientHello
              <span className="block text-xs text-ink-muted">
                Дробит TLS-рукопожатие, чтобы stateless-DPI не прочитал SNI
                (VLESS-Vision и Trojan-WS). Требует sing-box 1.12+.
              </span>
            </span>
            <Switch checked={anti.fragment} onChange={(v) => setAnti((a) => ({ ...a, fragment: v }))} />
          </label>
          <label className="flex items-center justify-between gap-3 py-3">
            <span className="text-sm">
              Блокировать QUIC (UDP/443)
              <span className="block text-xs text-ink-muted">
                Не даёт браузерному QUIC утечь мимо туннеля; может сломать
                приложения, которым QUIC обязателен.
              </span>
            </span>
            <Switch checked={anti.blockQuic} onChange={(v) => setAnti((a) => ({ ...a, blockQuic: v }))} />
          </label>
          <label className="flex items-center justify-between gap-3 py-3 last:pb-0">
            <span className="text-sm">
              Требовать TLS 1.3 на :443
              <span className="block text-xs text-ink-muted">
                Поднимает минимум TLS. Может чуть снизить правдоподобность
                сайта-заглушки для совсем старых клиентов.
              </span>
            </span>
            <Switch checked={anti.min13} onChange={(v) => setAnti((a) => ({ ...a, min13: v }))} />
          </label>
        </div>
      </div>

      {dirty && (
        <div className="flex items-center justify-end gap-2">
          <Button variant="light" color="gray" onClick={cancel} disabled={busy || applying}>
            Отменить
          </Button>
          <Button onClick={doSave} loading={busy || applying}>
            Сохранить
          </Button>
        </div>
      )}
      <ApplyingModal open={applying} />
    </div>
  );
}
