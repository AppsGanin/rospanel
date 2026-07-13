import { useEffect, useState } from "react";
import {
  createNode,
  deleteNode,
  getSettings,
  listNodes,
  regenNodeJoin,
  setNodeEnabled,
  updateNode,
  type NodeView,
} from "./api";
import { fmtBytes } from "./format";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  CenterLoader,
  Code,
  Modal,
  Select,
  Switch,
  TextInput,
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
  if (node.is_local) return <Badge color="brand">этот сервер</Badge>;
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
        Выполните на новом сервере (Ubuntu, от root). Нода подключится к панели и
        появится в списке. Токен показывается один раз — сохраните команду.
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

// AddNodeDialog collects a name + host and creates the node.
function AddNodeDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (command: string) => void;
}) {
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
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

  return (
    <Modal open onClose={onClose} title="Добавить ноду">
      <div className="space-y-3">
        <TextInput
          label="Название"
          value={name}
          onChange={setName}
          placeholder="Нидерланды #1"
        />
        <TextInput
          label="Домен или IP ноды"
          value={host}
          onChange={setHost}
          placeholder="nl1.example.com"
        />
        <p className="text-xs text-ink-muted">
          Домен или голый IP — в обоих случаях нода получит настоящий сертификат
          Let's Encrypt (для IP — короткоживущий, как на самой панели). Пока ACME
          не отработал, нода временно на самоподписанном, и панель вшивает в
          ссылки его отпечаток, чтобы клиенты подключались сразу.
        </p>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose}>
          Отмена
        </Button>
        <Button onClick={submit} loading={busy} disabled={!name.trim() || !host.trim()}>
          Создать
        </Button>
      </div>
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
  onChanged,
  onRegen,
}: {
  node: NodeView;
  decoys: string[];
  onChanged: () => void;
  onRegen: (command: string) => void;
}) {
  const { confirm, confirmNode } = useConfirm();

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

  const resetOverrides = async () => {
    try {
      await updateNode(node.id, {
        name: node.name,
        host: node.host,
        decoy_template: node.decoy_template,
        vless_enabled: null,
        trojan_enabled: null,
        hysteria_enabled: null,
        reality_enabled: null,
      });
      notifySuccess("Протоколы сброшены к глобальным");
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

  const hasOverrides =
    node.overrides.vless ||
    node.overrides.trojan ||
    node.overrides.hysteria ||
    node.overrides.reality;

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
          const overridden = node.overrides[p.key];
          return (
            <label key={p.key} className="flex items-center gap-2 text-sm">
              <Switch
                checked={enabled}
                disabled={node.is_local}
                onChange={(v) => patchProto(p.enabledField, v)}
              />
              <span className="text-ink">{p.label}</span>
              {!node.is_local && !overridden && (
                <span className="text-xs text-ink-muted">насл.</span>
              )}
            </label>
          );
        })}
      </div>

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
            {hasOverrides && (
              <Button size="sm" variant="light" color="gray" onClick={resetOverrides}>
                Сбросить протоколы
              </Button>
            )}
            <Button size="sm" variant="light" color="gray" onClick={regen}>
              Новый токен
            </Button>
            <Button size="sm" variant="light" color="red" onClick={remove}>
              Удалить
            </Button>
          </div>
        </div>
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
        <Button onClick={() => setAdding(true)}>Добавить ноду</Button>
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
        />
      )}
      {installCmd && (
        <InstallCommandModal command={installCmd} onClose={() => setInstallCmd(null)} />
      )}
    </div>
  );
}
