import { useEffect, useState } from "react";
import {
  createNode,
  deleteNode,
  getGeoCategories,
  getGeoStatus,
  getNodeLogs,
  getRouting,
  getSettings,
  listNodes,
  provisionNode,
  regenNodeJoin,
  saveRouting,
  setDecoy as saveDecoy,
  setMasterName,
  setMasterProtocols,
  setNodeEnabled,
  setNodeRouting,
  setXrayDNS,
  updateAllNodes,
  updateGeo,
  updateNode,
  updateNodeVersion,
  type GeoCategories,
  type GeoFile,
  type NodeView,
  type RoutingConfig,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { DnsEditor } from "./DnsEditor";
import { helperStatus } from "./EgressStatus";
import { fmtBytes } from "./format";
import { DECOY_LABELS } from "./GeneralSettings";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { TLSPanel } from "./TLSPanel";
import {
  effectiveCfg,
  EMPTY,
  GeoSection,
  hydrateRouting,
  laneSources,
  RoutingEditor,
  Section,
  type LaneSource,
  type StatusBadge,
} from "./RoutingEditor";
import {
  Badge,
  Button,
  Card,
  CenterLoader,
  cn,
  Code,
  Dropdown,
  DropdownDivider,
  DropdownItem,
  IconChevron,
  Modal,
  PasswordInput,
  Select,
  Switch,
  Textarea,
  TextInput,
  useConfirm,
} from "./ui";

// DialogTabs is the in-modal tab strip used by the server settings dialogs, so a
// server's many sections (domain / routing / DNS / …) don't stack into one long
// scroll. All tabs' state lives in the parent, so switching never loses edits and
// the single footer Save persists everything regardless of the active tab.
function DialogTabs({
  tabs,
  value,
  onChange,
}: {
  tabs: { value: string; label: string }[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="mb-4 flex gap-1 overflow-x-auto border-b border-gray-200">
      {tabs.map((t) => (
        <button
          key={t.value}
          onClick={() => onChange(t.value)}
          className={cn(
            "whitespace-nowrap border-b-2 px-3 py-2 text-sm font-semibold transition",
            value === t.value
              ? "border-brand-600 text-brand-800"
              : "border-transparent text-ink-muted hover:text-ink",
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

function fmtSeen(unix: number): string {
  if (!unix) return "ещё не подключалась";
  const ago = Math.floor(Date.now() / 1000) - unix;
  if (ago < 60) return "только что";
  if (ago < 3600) return `${Math.floor(ago / 60)} мин назад`;
  if (ago < 86400) return `${Math.floor(ago / 3600)} ч назад`;
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

// StatusBadge shows a node's connectivity. The local server (node 0) is always
// "this server"; a remote node is online/offline by its last sync, or "not joined"
// until the install command has run.
function StatusBadge({ node }: { node: NodeView }) {
  if (node.is_local) return <Badge color="brand">мастер</Badge>;
  if (!node.joined) return <Badge color="gray">не подключена</Badge>;
  if (node.online) return <Badge color="green">онлайн</Badge>;
  return <Badge color="red">офлайн</Badge>;
}

// InstallCommandModal shows the one-line install command exactly once after a node
// is created or its token is regenerated.
function InstallCommandModal({
  command,
  onClose,
}: {
  command: string;
  onClose: () => void;
}) {
  return (
    <Modal open onClose={onClose} title="Команда установки ноды" size="lg">
      <p className="text-sm text-ink-muted">
        Выполните на сервере ноды (Ubuntu, от root). Через минуту нода станет
        онлайн в списке. Токен показывается один раз — сохраните команду.
      </p>
      <div className="mt-3">
        <Code block copy>
          {command}
        </Code>
      </div>
      <div className="mt-4 flex justify-end">
        <Button onClick={onClose}>Готово</Button>
      </div>
    </Modal>
  );
}

// AddNodeDialog collects a name + host and creates the node, either handing back
// the copy-paste install command or (auto mode) installing it over SSH.
function AddNodeDialog({
  onClose,
  onCreated,
  onDone,
}: {
  onClose: () => void;
  onCreated: (command: string) => void;
  onDone: () => void;
}) {
  const [mode, setMode] = useState<"command" | "ssh">("command");
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [busy, setBusy] = useState(false);

  // SSH (auto) fields.
  const [sshHost, setSshHost] = useState("");
  const [sshPort, setSshPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [sshAuth, setSshAuth] = useState<"password" | "key">("password");
  const [sshPassword, setSshPassword] = useState("");
  const [sshKey, setSshKey] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [installing, setInstalling] = useState(false);

  const submitCommand = async () => {
    if (!name.trim() || !host.trim()) return;
    setBusy(true);
    try {
      const res = await createNode(name.trim(), host.trim());
      onCreated(res.install_command);
    } catch (e) {
      notifyError(errMessage(e));
      setBusy(false);
    }
  };

  const submitSSH = async () => {
    if (!name.trim() || !host.trim() || !sshHost.trim()) return;
    if (sshAuth === "password" && !sshPassword) return;
    if (sshAuth === "key" && !sshKey.trim()) return;
    setInstalling(true);
    setLog(["Создаём ноду…"]);
    try {
      const res = await createNode(name.trim(), host.trim());
      const outcome = await provisionNode(
        res.id,
        {
          ssh_host: sshHost.trim(),
          ssh_port: Number(sshPort) || 22,
          ssh_user: sshUser.trim(),
          ssh_password: sshAuth === "password" ? sshPassword : undefined,
          ssh_key: sshAuth === "key" ? sshKey : undefined,
        },
        (line) => setLog((l) => [...l, line]),
      );
      if (outcome === "done") {
        notifySuccess("Нода установлена по SSH");
        onDone();
      } else {
        notifyError("Установка завершилась с ошибкой — см. лог");
        setInstalling(false);
      }
    } catch (e) {
      setLog((l) => [...l, "ОШИБКА: " + errMessage(e)]);
      notifyError(errMessage(e));
      setInstalling(false);
    }
  };

  return (
    <Modal open onClose={onClose} title="Добавить ноду" size="lg">
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            key={m}
            onClick={() => setMode(m)}
            disabled={installing}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onaccent" : "text-ink-muted",
            )}
          >
            {m === "command" ? "Команда установки" : "Установить по SSH"}
          </button>
        ))}
      </div>

      <div className="space-y-3">
        <TextInput label="Название" value={name} onChange={setName} placeholder="Нидерланды #1" />
        <TextInput
          label="Домен или IP ноды"
          value={host}
          onChange={setHost}
          placeholder="nl1.example.com"
        />

        {mode === "ssh" && (
          <div className="space-y-3 border-t border-gray-100 pt-3">
            <p className="text-xs text-ink-muted">
              Панель зайдёт на сервер по SSH и установит ноду сама. Данные SSH
              нигде не сохраняются — используются только на время установки.
            </p>
            <div className="grid grid-cols-3 gap-2">
              <div className="col-span-2">
                <TextInput label="SSH-адрес (IP)" value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
              </div>
              <TextInput label="Порт" value={sshPort} onChange={setSshPort} placeholder="22" />
            </div>
            <TextInput label="SSH-пользователь" value={sshUser} onChange={setSshUser} placeholder="root" />
            <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
              {(["password", "key"] as const).map((a) => (
                <button
                  key={a}
                  onClick={() => setSshAuth(a)}
                  className={cn(
                    "rounded-md px-3 py-1 transition",
                    sshAuth === a ? "bg-brand-600 text-onaccent" : "text-ink-muted",
                  )}
                >
                  {a === "password" ? "Пароль" : "Ключ"}
                </button>
              ))}
            </div>
            {sshAuth === "password" ? (
              <PasswordInput label="SSH-пароль" value={sshPassword} onChange={setSshPassword} />
            ) : (
              <Textarea
                label="Приватный ключ (PEM)"
                value={sshKey}
                onChange={setSshKey}
                rows={4}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              />
            )}
          </div>
        )}

        {log.length > 0 && (
          <div className="max-h-56 overflow-auto rounded-md bg-gray-900 p-3 font-mono text-xs text-gray-100">
            {log.map((l, i) => (
              <div key={i} className={l.startsWith("ОШИБКА") ? "text-red-400" : ""}>
                {l}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={installing}>
          {installing ? "Закрыть" : "Отмена"}
        </Button>
        {mode === "command" ? (
          <Button onClick={submitCommand} loading={busy} disabled={!name.trim() || !host.trim()}>
            Создать
          </Button>
        ) : (
          <Button
            onClick={submitSSH}
            loading={installing}
            disabled={!name.trim() || !host.trim() || !sshHost.trim()}
          >
            Установить
          </Button>
        )}
      </div>
    </Modal>
  );
}

// ReconnectDialog re-installs a node that isn't connected — it SSHes back into the
// server and re-runs the install with a fresh token, streaming the log (which also
// surfaces why the previous attempt didn't connect). SSH creds aren't stored.
function ReconnectDialog({
  node,
  onClose,
  onDone,
}: {
  node: NodeView;
  onClose: () => void;
  onDone: () => void;
}) {
  const [sshHost, setSshHost] = useState(node.host);
  const [sshPort, setSshPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [sshAuth, setSshAuth] = useState<"password" | "key">("password");
  const [sshPassword, setSshPassword] = useState("");
  const [sshKey, setSshKey] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [running, setRunning] = useState(false);

  const run = async () => {
    if (!sshHost.trim()) return;
    if (sshAuth === "password" && !sshPassword) return;
    if (sshAuth === "key" && !sshKey.trim()) return;
    setRunning(true);
    setLog(["Переустанавливаем ноду…"]);
    try {
      const outcome = await provisionNode(
        node.id,
        {
          ssh_host: sshHost.trim(),
          ssh_port: Number(sshPort) || 22,
          ssh_user: sshUser.trim(),
          ssh_password: sshAuth === "password" ? sshPassword : undefined,
          ssh_key: sshAuth === "key" ? sshKey : undefined,
        },
        (line) => setLog((l) => [...l, line]),
      );
      if (outcome === "done") {
        notifySuccess("Нода переустановлена — подключится в течение минуты");
        onDone();
      } else {
        notifyError("Не удалось — см. лог");
        setRunning(false);
      }
    } catch (e) {
      setLog((l) => [...l, "ОШИБКА: " + errMessage(e)]);
      notifyError(errMessage(e));
      setRunning(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={`Переустановить «${node.name}»`} size="lg">
      <div className="space-y-3">
        <p className="text-xs text-ink-muted">
          Панель зайдёт на сервер ноды по SSH и переустановит агент с новым токеном.
          Данные SSH нигде не сохраняются.
        </p>
        <div className="grid grid-cols-3 gap-2">
          <div className="col-span-2">
            <TextInput label="SSH-адрес (IP)" value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
          </div>
          <TextInput label="Порт" value={sshPort} onChange={setSshPort} placeholder="22" />
        </div>
        <TextInput label="SSH-пользователь" value={sshUser} onChange={setSshUser} placeholder="root" />
        <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
          {(["password", "key"] as const).map((a) => (
            <button
              key={a}
              onClick={() => setSshAuth(a)}
              className={cn(
                "rounded-md px-3 py-1 transition",
                sshAuth === a ? "bg-brand-600 text-onaccent" : "text-ink-muted",
              )}
            >
              {a === "password" ? "Пароль" : "Ключ"}
            </button>
          ))}
        </div>
        {sshAuth === "password" ? (
          <PasswordInput label="SSH-пароль" value={sshPassword} onChange={setSshPassword} />
        ) : (
          <Textarea
            label="Приватный ключ (PEM)"
            value={sshKey}
            onChange={setSshKey}
            rows={4}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          />
        )}
        {log.length > 0 && (
          <div className="max-h-56 overflow-auto rounded-md bg-gray-900 p-3 font-mono text-xs text-gray-100">
            {log.map((l, i) => (
              <div key={i} className={l.startsWith("ОШИБКА") ? "text-red-400" : ""}>
                {l}
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={running}>
          {running ? "Закрыть" : "Отмена"}
        </Button>
        <Button onClick={run} loading={running} disabled={!sshHost.trim()}>
          Переустановить
        </Button>
      </div>
    </Modal>
  );
}

// nodeDefaultRouting is a fresh node routing override: the full editor's default
// (block/direct/lanes/WARP/Opera), with ad-blocking on — the operator just enabled
// "own routing", so give them a sensible starting point.
function nodeDefaultRouting(): RoutingConfig {
  return { ...hydrateRouting(null), block_ads: true };
}

// useServerRouting holds the editable routing + egress state shared by the node and
// master settings dialogs, so both drive the same RoutingEditor. The container owns
// saving; this only manages the in-progress edit (and lane-source flip state).
function useServerRouting(init: {
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
    effective: () => effectiveCfg(cfg, laneSrc),
    // reset re-seeds every field (used by the master dialog after its async load).
    reset: (c: RoutingConfig, w: boolean, o: boolean, cc: string) => {
      setCfg(c);
      setLaneSrc(laneSources(c.lanes));
      setWarpEnabled(w);
      setOperaEnabled(o);
      setOperaCountry(cc || "EU");
    },
  };
}

// NodeDomainCard is the node's Домен tab, mirroring the master's TLSPanel: a
// current-address card with the cert status, plus a "сменить домен" action. The node
// gets its cert itself (ACME on the node, using the panel's ACME settings), so unlike
// the master there's no email/CA field here and no page redirect — changing the host
// just re-points the node and it re-issues the cert.
function NodeDomainCard({
  node,
  onChanged,
}: {
  node: NodeView;
  onChanged: () => void;
}) {
  const [host, setHost] = useState(node.host);
  const [busy, setBusy] = useState(false);
  const t = host.trim();
  const dirty = t !== "" && t !== node.host;

  const change = async () => {
    if (!dirty) return;
    setBusy(true);
    try {
      // Change only the host; carry the node's other current fields so the patch
      // doesn't touch them.
      await updateNode(node.id, {
        name: node.name,
        host: t,
        decoy_template: node.decoy_template,
        vless_enabled: node.vless_enabled,
        trojan_enabled: node.trojan_enabled,
        hysteria_enabled: node.hysteria_enabled,
        reality_enabled: node.reality_enabled,
      });
      notifySuccess("Домен ноды изменён — нода перевыпустит сертификат для нового адреса");
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
    setBusy(false);
  };

  return (
    <div className="flex flex-col gap-4">
      <Section>
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm text-ink-muted">Текущий адрес</p>
            <p className="truncate text-lg font-bold text-ink">{node.host}</p>
          </div>
          <Badge color={node.cert_self_signed ? "orange" : "green"}>
            {node.cert_self_signed ? "временный сертификат" : "валидный сертификат"}
          </Badge>
        </div>
      </Section>

      <Section
        title="Сменить домен"
        desc="Укажите домен, направленный на этот сервер, или его IP. Нужен открытый порт 80. Нода сама перевыпустит сертификат (по настройкам ACME панели). Адрес меняется и в ссылках подписки."
      >
        <TextInput
          label="Новый домен или IP"
          value={host}
          onChange={setHost}
          placeholder="nl1.example.com"
        />
        <div className="flex justify-end">
          <Button loading={busy} disabled={!dirty} onClick={change}>
            Сменить домен
          </Button>
        </div>
      </Section>
    </div>
  );
}

// NodeSettingsDialog edits a remote node's full per-server config: name, decoy,
// protocol overrides, its OWN routing + egress (the same editor as the master), and
// its DNS. Routing/egress and DNS each either inherit the panel's or are the node's
// own override. Egress (proxy lanes / WARP / Opera) is independent of the master and
// only meaningful with own routing, so it lives inside the routing editor.
function NodeSettingsDialog({
  node,
  decoys,
  geo,
  onClose,
  onSaved,
  onRefresh,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onClose: () => void;
  onSaved: () => void;
  onRefresh: () => void;
}) {
  const [name, setName] = useState(node.name);
  const [decoy, setDecoy] = useState(node.decoy_template);
  // Each node's protocols are its OWN (no inheritance from the master). A fresh node
  // starts with everything off; the operator turns on what this node should serve.
  const [proto, setProto] = useState({
    vless: node.vless_enabled,
    trojan: node.trojan_enabled,
    hysteria: node.hysteria_enabled,
    reality: node.reality_enabled,
  });
  const r = useServerRouting({
    cfg: node.routing ? hydrateRouting(node.routing) : nodeDefaultRouting(),
    warp: node.warp_enabled,
    opera: node.opera_enabled,
    country: node.opera_country,
  });
  const [dns, setDns] = useState(node.xray_dns ?? "");
  const [saving, setSaving] = useState(false);
  const [tab, setTab] = useState("general");

  const toggleProto = (k: "vless" | "trojan" | "hysteria" | "reality", v: boolean) =>
    setProto((p) => ({ ...p, [k]: v }));

  // Status badges: WARP registration is known from the node's report; Opera runs
  // remotely, so the panel only shows enabled/disabled.
  const warpBadge: StatusBadge = !r.warpEnabled
    ? { label: "выключен", color: "gray" }
    : node.warp_registered
      ? { label: "активен", color: "green" }
      : { label: "будет зарегистрирован", color: "orange" };
  const operaBadge: StatusBadge = r.operaEnabled
    ? { label: "включён", color: "green" }
    : { label: "выключен", color: "gray" };

  const save = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await updateNode(node.id, {
        name: name.trim(),
        host: node.host, // the domain is changed from the Домен tab, not here
        decoy_template: decoy,
        vless_enabled: proto.vless,
        trojan_enabled: proto.trojan,
        hysteria_enabled: proto.hysteria,
        reality_enabled: proto.reality,
      });
      // Routing + egress in one call — always the node's OWN (no inherit toggle).
      // An empty routing config just means "mostly direct"; empty DNS ⇒ default resolver.
      await setNodeRouting(
        node.id,
        r.effective(),
        dns.trim() ? dns : null,
        r.warpEnabled,
        r.operaEnabled,
        r.operaCountry,
      );
      notifySuccess("Настройки ноды сохранены");
      onSaved();
    } catch (e) {
      notifyError(errMessage(e));
      setSaving(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={`Настройки — «${node.name}»`} size="xl">
      <DialogTabs
        value={tab}
        onChange={setTab}
        tabs={[
          { value: "general", label: "Основное" },
          { value: "routing", label: "Роутинг" },
          { value: "domain", label: "Домен" },
        ]}
      />

      {tab === "general" && (
        <div className="flex flex-col gap-4">
          <Section title="Сервер">
            <TextInput label="Название" value={name} onChange={setName} placeholder="Нидерланды #1" />
            <Select
              label="Заглушка"
              value={decoy}
              onChange={setDecoy}
              data={decoys.map((d) => ({ value: d, label: DECOY_LABELS[d] ?? d }))}
            />
          </Section>

          <Section
            title="Протоколы"
            desc="Какие протоколы обслуживает эта нода. Порты и транспорт — во вкладке «Подключения» в настройках."
          >
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
              {protoDefs.map((p) => (
                <label key={p.key} className="flex items-center gap-2 text-sm">
                  <Switch checked={proto[p.key]} onChange={(v) => toggleProto(p.key, v)} />
                  <span className="text-ink">{p.label}</span>
                </label>
              ))}
            </div>
          </Section>
        </div>
      )}

      {tab === "routing" && (
        <div className="flex flex-col gap-4">
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
            applying={saving}
            liveStatus={false}
          />
          <Section title="DNS" desc="Резолвер, который использует нода. Пусто — по умолчанию.">
            <DnsEditor value={dns} onChange={setDns} />
          </Section>
        </div>
      )}

      {tab === "domain" && <NodeDomainCard node={node} onChanged={onRefresh} />}

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={saving}>
          Отмена
        </Button>
        <Button onClick={save} loading={saving}>
          Сохранить
        </Button>
      </div>
    </Modal>
  );
}

// MasterNameEditor lets the operator name the master server for config labels
// (shown as "<имя> · VLESS…" in clients). Empty = no prefix.
// MasterSettingsDialog holds the master server's per-server settings. The master's
// protocols, decoy, routing and DNS are the panel's GLOBAL settings (edited in their
// own tabs), so here we only set its config-label name and point at the rest.
function MasterSettingsDialog({
  node,
  decoys,
  geo,
  onClose,
  onSaved,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { applying, apply } = useXrayApply();
  const [loaded, setLoaded] = useState(false);
  const [name, setName] = useState(node.master_label ?? "");
  const [decoy, setDecoy] = useState(node.decoy_template);
  const [dns, setDns] = useState(node.xray_dns ?? "");
  // The master's protocols on/off (like a node). Connection details stay in the
  // global Подключения settings; only the toggle lives here.
  const [proto, setProto] = useState({
    vless: node.vless_enabled,
    trojan: node.trojan_enabled,
    hysteria: node.hysteria_enabled,
    reality: node.reality_enabled,
  });
  // Live egress status for the badges (master's egress runs locally, so the panel
  // knows the real state — unlike a node).
  const [warpRegistered, setWarpRegistered] = useState(node.warp_registered);
  const [operaRunning, setOperaRunning] = useState(false);
  const [operaAlive, setOperaAlive] = useState(false);
  const [proxyCounts, setProxyCounts] = useState<Record<string, number>>({});
  const [geoStatus, setGeoStatus] = useState<GeoFile[]>([]);
  const [tab, setTab] = useState("general");
  const r = useServerRouting({
    cfg: EMPTY,
    warp: node.warp_enabled,
    opera: node.opera_enabled,
    country: node.opera_country,
  });
  const reset = r.reset;

  useEffect(() => {
    getGeoStatus().then(setGeoStatus).catch(() => {});
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
      })
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const refreshGeo = () =>
    apply(async () => {
      setGeoStatus(await updateGeo());
      notifySuccess("Geo-базы обновлены");
    });

  const warpBadge: StatusBadge = !r.warpEnabled
    ? { label: "выключен", color: "gray" }
    : warpRegistered
      ? { label: "активен", color: "green" }
      : { label: "не зарегистрирован", color: "orange" };
  const operaBadge = helperStatus(
    r.operaEnabled,
    operaRunning,
    operaAlive,
    "",
  ) as StatusBadge;

  const save = () =>
    apply(async () => {
      await setMasterName(name.trim());
      await setMasterProtocols({
        vless_enabled: proto.vless,
        trojan_enabled: proto.trojan,
        hysteria_enabled: proto.hysteria,
        reality_enabled: proto.reality,
      });
      await saveDecoy(decoy);
      await setXrayDNS(dns);
      // Routing + WARP/Opera together (one reconcile).
      await saveRouting(r.effective(), r.warpEnabled, r.operaEnabled, r.operaCountry);
      notifySuccess("Настройки мастера сохранены");
      onSaved();
    });

  return (
    <Modal open onClose={onClose} title="Настройки — мастер" size="xl">
      {!loaded ? (
        <CenterLoader />
      ) : (
        <>
          <DialogTabs
            value={tab}
            onChange={setTab}
            tabs={[
              { value: "general", label: "Основное" },
              { value: "routing", label: "Роутинг" },
              { value: "geo", label: "Geo" },
              { value: "domain", label: "Домен" },
            ]}
          />

          {tab === "general" && (
            <div className="flex flex-col gap-4">
              <Section title="Сервер" desc="Имя в конфигах и сайт-заглушка мастера.">
                <div>
                  <TextInput
                    label="Имя в конфигах"
                    value={name}
                    onChange={setName}
                    placeholder="напр. Мастер (пусто — без префикса)"
                  />
                  <p className="mt-1 text-xs text-ink-muted">
                    Показывается в клиенте как «‹имя› · VLESS…». Пусто — без префикса.
                  </p>
                </div>
                <Select
                  label="Заглушка"
                  value={decoy}
                  onChange={setDecoy}
                  data={decoys.map((d) => ({ value: d, label: DECOY_LABELS[d] ?? d }))}
                />
              </Section>

              <Section
                title="Протоколы"
                desc="Какие протоколы обслуживает мастер. Порты и транспорт — во вкладке «Подключения» в настройках."
              >
                <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
                  {protoDefs.map((p) => (
                    <label key={p.key} className="flex items-center gap-2 text-sm">
                      <Switch
                        checked={proto[p.key]}
                        onChange={(v) => setProto((s) => ({ ...s, [p.key]: v }))}
                      />
                      <span className="text-ink">{p.label}</span>
                    </label>
                  ))}
                </div>
              </Section>
            </div>
          )}

          {tab === "routing" && (
            <div className="flex flex-col gap-4">
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
                proxyCounts={proxyCounts}
                geosite={geo.geosite}
                geoip={geo.geoip}
                applying={applying}
              />
              <Section title="DNS" desc="Резолвер, который использует Xray. Пусто — по умолчанию.">
                <DnsEditor value={dns} onChange={setDns} />
              </Section>
            </div>
          )}

          {tab === "geo" && (
            <GeoSection
              status={geoStatus}
              onRefresh={refreshGeo}
              refreshing={applying}
            />
          )}

          {/* Domain / TLS — its own load + "сменить домен" button (page redirects
              on success), independent of this dialog's Save. */}
          {tab === "domain" && <TLSPanel />}

          <div className="mt-5 flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={onClose} disabled={applying}>
              Отмена
            </Button>
            <Button onClick={save} loading={applying}>
              Сохранить
            </Button>
          </div>
        </>
      )}
      <ApplyingModal open={applying} />
    </Modal>
  );
}

// protoDefs drives the four protocol toggles on a node card.
const protoDefs = [
  { key: "vless", label: "VLESS", enabledField: "vless_enabled" },
  { key: "trojan", label: "Trojan", enabledField: "trojan_enabled" },
  { key: "hysteria", label: "Hysteria2", enabledField: "hysteria_enabled" },
  { key: "reality", label: "REALITY", enabledField: "reality_enabled" },
] as const;

// NodeCard renders one node with its status, traffic, protocol toggles and decoy.
function NodeCard({
  node,
  decoys,
  geo,
  onChanged,
  onRegen,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onChanged: () => void;
  onRegen: (command: string) => void;
}) {
  const { confirm, confirmNode } = useConfirm();
  const [reconnecting, setReconnecting] = useState(false);
  const [editingRouting, setEditingRouting] = useState(false);
  const [showingLogs, setShowingLogs] = useState(false);

  const toggleEnabled = async (enabled: boolean) => {
    try {
      await setNodeEnabled(node.id, enabled);
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const remove = async () => {
    if (
      !(await confirm({
        title: "Удалить ноду?",
        body: `«${node.name}» перестанет обслуживать пользователей. Историю трафика можно оставить.`,
        confirmLabel: "Удалить",
        danger: true,
      }))
    )
      return;
    try {
      await deleteNode(node.id);
      notifySuccess("Нода удалена");
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const regen = async () => {
    if (
      !(await confirm({
        title: "Новый токен установки?",
        body: "Текущая установка ноды перестанет подключаться, пока вы не переустановите её новой командой.",
        confirmLabel: "Сгенерировать",
      }))
    )
      return;
    try {
      const res = await regenNodeJoin(node.id);
      onRegen(res.install_command);
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const doUpdate = async () => {
    try {
      await updateNodeVersion(node.id);
      notifySuccess("Нода обновляется — Xray перезапустится");
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <Card className="p-4">
      {confirmNode}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-semibold text-ink">{node.name}</span>
            <StatusBadge node={node} />
            {!node.enabled && !node.is_local && <Badge color="gray">выключена</Badge>}
          </div>
          <div className="mt-0.5 truncate text-sm text-ink-muted">{node.host}</div>
        </div>
        {!node.is_local && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-ink-muted">включена</span>
            <Switch checked={node.enabled} onChange={toggleEnabled} />
          </div>
        )}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4">
        <Meta label="Трафик сегодня" value={fmtBytes(node.traffic_up + node.traffic_down)} />
        {!node.is_local && <Meta label="Последний контакт" value={fmtSeen(node.last_seen)} />}
        <Meta
          label="Xray"
          value={
            <span className={node.version_skew ? "text-amber-600" : undefined}>
              {node.xray_version || "—"}
              {node.version_skew ? " ⚠" : ""}
            </span>
          }
        />
        {!node.is_local && <Meta label="Агент" value={node.node_version || "—"} />}
      </div>

      <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
        <Button size="sm" variant="light" color="gray" onClick={() => setEditingRouting(true)}>
          Настройки
        </Button>
        {!node.is_local && (
          <>
            <Button size="sm" variant="light" color="gray" onClick={() => setShowingLogs(true)}>
              Логи
            </Button>
            <Dropdown
              align="end"
              width={210}
              trigger={
                <span className="inline-flex items-center gap-1 rounded-lg border border-gray-200 px-3 py-1.5 text-sm font-medium text-ink transition hover:bg-gray-50">
                  Управление
                  <IconChevron className="h-3.5 w-3.5" />
                </span>
              }
            >
              <DropdownItem onClick={doUpdate}>
                Обновить{node.version_skew ? " (новая версия)" : ""}
              </DropdownItem>
              <DropdownItem onClick={() => setReconnecting(true)}>
                Переустановить
              </DropdownItem>
              <DropdownItem onClick={regen}>Новый токен</DropdownItem>
              <DropdownDivider />
              <DropdownItem color="red" onClick={remove}>
                Удалить
              </DropdownItem>
            </Dropdown>
          </>
        )}
      </div>
      {reconnecting && (
        <ReconnectDialog
          node={node}
          onClose={() => setReconnecting(false)}
          onDone={() => {
            setReconnecting(false);
            onChanged();
          }}
        />
      )}
      {editingRouting &&
        (node.is_local ? (
          <MasterSettingsDialog
            node={node}
            decoys={decoys}
            geo={geo}
            onClose={() => setEditingRouting(false)}
            onSaved={() => {
              setEditingRouting(false);
              onChanged();
            }}
          />
        ) : (
          <NodeSettingsDialog
            node={node}
            decoys={decoys}
            geo={geo}
            onClose={() => setEditingRouting(false)}
            onSaved={() => {
              setEditingRouting(false);
              onChanged();
            }}
            onRefresh={onChanged}
          />
        ))}
      {showingLogs && (
        <NodeLogsDialog node={node} onClose={() => setShowingLogs(false)} />
      )}
    </Card>
  );
}

// NodeLogsDialog streams a node's recent logs. It polls the panel, which asks the
// node to include its log tail on its next sync (agent + Xray), so the view stays
// fresh while open (with up to one sync interval of latency).
function NodeLogsDialog({ node, onClose }: { node: NodeView; onClose: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let alive = true;
    const poll = () =>
      getNodeLogs(node.id)
        .then((r) => {
          if (!alive) return;
          setLines(r.lines);
          setLoaded(true);
        })
        .catch(() => {});
    poll();
    const t = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [node.id]);

  return (
    <Modal open onClose={onClose} title={`Логи — «${node.name}»`} size="xl">
      {!loaded ? (
        <p className="text-sm text-ink-muted">Запрашиваем логи у ноды…</p>
      ) : lines.length === 0 ? (
        <p className="text-sm text-ink-muted">
          Логи пока не получены. Нода пришлёт их при следующей синхронизации (в течение
          минуты) — подождите.
        </p>
      ) : (
        <div className="max-h-[60vh] overflow-auto rounded-md bg-gray-900 p-3 font-mono text-xs leading-relaxed text-gray-100">
          {lines.map((l, i) => (
            <div key={i} className="whitespace-pre-wrap break-all">
              {l}
            </div>
          ))}
        </div>
      )}
      <div className="mt-4 flex justify-end">
        <Button variant="light" color="gray" onClick={onClose}>
          Закрыть
        </Button>
      </div>
    </Modal>
  );
}

function Meta({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-ink-muted">{label}</div>
      <div className="truncate text-ink">{value}</div>
    </div>
  );
}

export function NodesPanel() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
  // Geo categories feed the routing editor's domain/IP suggestions (same list for
  // the master and every node — one panel-side geosite/geoip).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [] });
  const [adding, setAdding] = useState(false);
  const [installCmd, setInstallCmd] = useState<string | null>(null);

  const load = () =>
    listNodes()
      .then((r) => setNodes(r.nodes))
      .catch((e) => notifyError(errMessage(e)));

  useEffect(() => {
    load();
    getSettings()
      .then((s) => setDecoys(s.decoy_templates || []))
      .catch(() => {});
    getGeoCategories()
      .then((g) => setGeo({ geosite: g.geosite ?? [], geoip: g.geoip ?? [] }))
      .catch(() => {});
    // Refresh liveness periodically so online/offline badges stay current.
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, []);

  if (nodes === null) return <CenterLoader />;

  const remoteCount = nodes.filter((n) => !n.is_local).length;
  const anyStale = nodes.some((n) => !n.is_local && n.version_skew && n.online);

  const updateAll = async () => {
    try {
      const r = await updateAllNodes();
      notifySuccess(`Обновление запущено на нодах: ${r.nodes}`);
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-ink">Сервера</h1>
          <p className="text-sm text-ink-muted">
            {remoteCount === 0
              ? "Все пользователи обслуживаются этим сервером. Добавьте ноду, чтобы раздать нагрузку и локации."
              : `Пользователи синхронизируются на все включённые ноды (${remoteCount}).`}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {anyStale && (
            <Button variant="light" color="gray" onClick={updateAll}>
              Обновить все ноды
            </Button>
          )}
          <Button onClick={() => setAdding(true)}>Добавить ноду</Button>
        </div>
      </div>

      <div className="space-y-3">
        {nodes.map((n) => (
          <NodeCard
            key={n.id}
            node={n}
            decoys={decoys}
            geo={geo}
            onChanged={load}
            onRegen={setInstallCmd}
          />
        ))}
      </div>

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
