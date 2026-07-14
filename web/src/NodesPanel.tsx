import { useEffect, useState } from "react";
import {
  createNode,
  deleteNode,
  getSettings,
  listNodes,
  provisionNode,
  regenNodeJoin,
  setMasterName,
  setNodeEnabled,
  setNodeRouting,
  updateAllNodes,
  updateNode,
  updateNodeVersion,
  type NodeView,
  type RoutingConfig,
} from "./api";
import { fmtBytes } from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  CenterLoader,
  cn,
  Code,
  Modal,
  PasswordInput,
  Select,
  Switch,
  TagsInput,
  Textarea,
  TextInput,
  ToggleRow,
  useConfirm,
} from "./ui";

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

// emptyNodeRouting is a blank routing override (only the fields that work on a node
// — egress lanes/WARP/Opera are stripped server-side, so they're not editable here).
function emptyNodeRouting(): RoutingConfig {
  return {
    block_bittorrent: false,
    block_ads: true,
    block_ips: [],
    block_domains: [],
    warp_domains: [],
    warp_ips: [],
    opera_domains: [],
    opera_ips: [],
    direct_domains: [],
    direct_ips: [],
    routing_order: [],
    lanes: [],
    proxy_refresh_minutes: 0,
  };
}

// NodeRoutingDialog edits a node's routing + DNS overrides. Each section can either
// inherit the panel's setting or be a per-node override. Only the block/direct rules
// apply on a node (proxy lanes / WARP / Opera egress don't exist there), so only
// those are exposed.
function NodeRoutingDialog({
  node,
  onClose,
  onSaved,
}: {
  node: NodeView;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [routingOwn, setRoutingOwn] = useState(node.routing != null);
  const [cfg, setCfg] = useState<RoutingConfig>(node.routing ?? emptyNodeRouting());
  const [dnsOwn, setDnsOwn] = useState(node.xray_dns != null);
  const [dns, setDns] = useState(node.xray_dns ?? "");
  const [saving, setSaving] = useState(false);

  const set = (patch: Partial<RoutingConfig>) => setCfg((c) => ({ ...c, ...patch }));

  const save = async () => {
    setSaving(true);
    try {
      await setNodeRouting(
        node.id,
        routingOwn ? cfg : null,
        dnsOwn ? dns : null,
      );
      notifySuccess("Роутинг и DNS ноды сохранены");
      onSaved();
    } catch (e) {
      notifyError(errMessage(e));
      setSaving(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={`Блокировки и DNS — «${node.name}»`} size="lg">
      <div className="space-y-4">
        {/* Routing */}
        <ToggleRow
          label="Свои блокировки ноды"
          hint="Выключено — нода использует роутинг панели. Весь трафик ноды идёт напрямую (прокси-полосы/WARP/Opera на нодах не работают), поэтому здесь только блокировки."
          checked={routingOwn}
          onChange={setRoutingOwn}
        />
        {routingOwn && (
          <div className="space-y-3 rounded-lg border border-gray-100 p-3">
            <ToggleRow
              label="Блокировать рекламу"
              checked={cfg.block_ads}
              onChange={(v) => set({ block_ads: v })}
            />
            <ToggleRow
              label="Блокировать BitTorrent"
              checked={cfg.block_bittorrent}
              onChange={(v) => set({ block_bittorrent: v })}
            />
            <TagsInput
              label="Блокировать домены"
              value={cfg.block_domains}
              onChange={(v) => set({ block_domains: v })}
              placeholder="example.com, geosite:category-ads…"
            />
            <TagsInput
              label="Блокировать IP / CIDR"
              value={cfg.block_ips}
              onChange={(v) => set({ block_ips: v })}
              placeholder="1.2.3.0/24, geoip:cn…"
            />
          </div>
        )}

        {/* DNS */}
        <ToggleRow
          label="Свой DNS ноды"
          hint="Выключено — нода использует DNS панели."
          checked={dnsOwn}
          onChange={setDnsOwn}
        />
        {dnsOwn && (
          <Textarea
            label="DNS-серверы"
            value={dns}
            onChange={setDns}
            rows={3}
            placeholder={"https://1.1.1.1/dns-query\n8.8.8.8"}
            hint="По одному на строку (или через запятую): DoH URL или IP."
          />
        )}
      </div>
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
function MasterNameEditor({
  current,
  onSaved,
}: {
  current: string;
  onSaved: () => void;
}) {
  const [name, setName] = useState(current);
  const [saving, setSaving] = useState(false);
  const save = async () => {
    setSaving(true);
    try {
      await setMasterName(name.trim());
      notifySuccess("Имя мастера сохранено");
      onSaved();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="mt-4 flex flex-wrap items-end gap-2">
      <div className="w-64 max-w-full">
        <TextInput
          label="Имя в конфигах"
          value={name}
          onChange={setName}
          placeholder="напр. Мастер (пусто — без префикса)"
        />
      </div>
      <Button size="sm" variant="light" color="gray" onClick={save} loading={saving} disabled={name.trim() === current.trim()}>
        Сохранить
      </Button>
    </div>
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
  onChanged,
  onRegen,
}: {
  node: NodeView;
  decoys: string[];
  onChanged: () => void;
  onRegen: (command: string) => void;
}) {
  const { confirm, confirmNode } = useConfirm();
  const [reconnecting, setReconnecting] = useState(false);
  const [editingRouting, setEditingRouting] = useState(false);

  // A protocol toggle sets an explicit override on this node (never touches the
  // global setting). The local server's protocols are edited in Settings, so its
  // toggles are read-only here.
  const patchProto = async (field: string, value: boolean) => {
    try {
      await updateNode(node.id, {
        name: node.name,
        host: node.host,
        decoy_template: node.decoy_template,
        vless_enabled: field === "vless_enabled" ? value : overrideVal(node, "vless"),
        trojan_enabled: field === "trojan_enabled" ? value : overrideVal(node, "trojan"),
        hysteria_enabled:
          field === "hysteria_enabled" ? value : overrideVal(node, "hysteria"),
        reality_enabled: field === "reality_enabled" ? value : overrideVal(node, "reality"),
      });
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const patchDecoy = async (decoy: string) => {
    try {
      await updateNode(node.id, {
        name: node.name,
        host: node.host,
        decoy_template: decoy,
        vless_enabled: overrideVal(node, "vless"),
        trojan_enabled: overrideVal(node, "trojan"),
        hysteria_enabled: overrideVal(node, "hysteria"),
        reality_enabled: overrideVal(node, "reality"),
      });
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

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

      {/* Protocol toggles. On the local server they mirror Settings (read-only). */}
      <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2">
        {protoDefs.map((p) => {
          const enabled = node[p.enabledField] as boolean;
          // const overridden = node.overrides[p.key];
          return (
            <label key={p.key} className="flex items-center gap-2 text-sm">
              <Switch
                checked={enabled}
                disabled={node.is_local}
                onChange={(v) => patchProto(p.enabledField, v)}
              />
              <span className="text-ink">{p.label}</span>
            </label>
          );
        })}
      </div>

      {node.is_local && (
        <MasterNameEditor current={node.master_label ?? ""} onSaved={onChanged} />
      )}

      {!node.is_local && (
        <div className="mt-4 flex flex-wrap items-end justify-between gap-3">
          <div className="w-52 max-w-full">
            <Select
              label="Заглушка"
              value={node.decoy_template}
              onChange={patchDecoy}
              data={decoys.map((d) => ({ value: d, label: d }))}
            />
          </div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="light" color="brand" onClick={() => setReconnecting(true)}>
              Переустановить
            </Button>
            {node.version_skew && node.online && (
              <Button size="sm" variant="light" color="brand" onClick={doUpdate}>
                Обновить
              </Button>
            )}
            <Button size="sm" variant="light" color="gray" onClick={() => setEditingRouting(true)}>
              Блокировки и DNS
            </Button>
            <Button size="sm" variant="light" color="gray" onClick={regen}>
              Новый токен
            </Button>
            <Button size="sm" variant="light" color="red" onClick={remove}>
              Удалить
            </Button>
          </div>
        </div>
      )}
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
      {editingRouting && (
        <NodeRoutingDialog
          node={node}
          onClose={() => setEditingRouting(false)}
          onSaved={() => {
            setEditingRouting(false);
            onChanged();
          }}
        />
      )}
    </Card>
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

// overrideVal returns the node's current override for a protocol (null when it
// inherits the global setting), so a patch that changes one field preserves the
// override state of the others.
function overrideVal(node: NodeView, key: "vless" | "trojan" | "hysteria" | "reality") {
  if (!node.overrides[key]) return null;
  return node[`${key}_enabled` as const] as boolean;
}

export function NodesPanel() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
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
          <h1 className="text-xl font-semibold text-ink">Ноды</h1>
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
