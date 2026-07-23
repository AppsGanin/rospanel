import { useEffect, useState } from "react";
import {
  applyConnections,
  applyNodeConnections,
  createNode,
  deleteNode,
  getConnections,
  getGeoCategories,
  getNodeConnections,
  getNodeGeo,
  getNodeTLS,
  getGeoStatus,
  getNodeLogs,
  getRouting,
  getSettings,
  listNodes,
  provisionNode,
  refreshNodeGeo,
  regenNodeJoin,
  saveRouting,
  setNodeACME,
  setNodeGeoCadence,
  setDecoy as saveDecoy,
  setGeoCadence as saveGeoCadence,
  setMasterName,
  setNodeDNS,
  setNodeEnabled,
  setNodeRouting,
  setXrayDNS,
  updateAllNodes,
  updateGeo,
  updateIPLists,
  setIPListCadence as saveIPListCadence,
  restartNodeXray,
  restartXray,
  updateNode,
  updateNodeVersion,
  type GeoCategories,
  type GeoFile,
  type GeoInfo,
  type NodeView,
  type RoutingConfig,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { ConnectionsEditor } from "./ConnectionsEditor";
import { canonicalDns, DnsEditor } from "./DnsEditor";
import { helperStatus } from "./egress";
import { fmtBytes } from "./format";
import { DECOY_LABELS } from "./GeneralSettings";
import { HealthPanel } from "./HealthPanel";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { TLSPanel } from "./TLSPanel";
import { XrayConfigView } from "./XrayConfig";
import { XrayLogs } from "./XrayLogs";
import {
  effectiveCfg,
  EMPTY,
  GeoSection,
  IPListSection,
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
  IconBraces,
  IconButton,
  IconDots,
  IconGear,
  IconPulse,
  IconRestart,
  IconTerminal,
  Modal,
  PasswordInput,
  SegmentedControl,
  Select,
  Switch,
  TagsInput,
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

// TabSaveBar is the inline save row for the "form" tabs (Основное / Роутинг / DNS)
// of the server dialogs, so each tab commits on its own — exactly like the
// Подключения / Geo / Домен tabs already do, staying open after save. "Отменить"
// reverts unsaved edits to the last-saved state; both buttons disable when there's
// nothing to save. The note spells out that edits are staged and per-section.
function TabSaveBar({
  onSave,
  onReset,
  dirty,
  busy,
}: {
  onSave: () => void;
  onReset: () => void;
  dirty: boolean;
  busy: boolean;
}) {
  return (
    <div className="flex flex-col gap-2 border-t border-gray-200 pt-4 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-ink-muted">
        У каждого раздела своё сохранение. Без сохранения изменения не применятся.
      </p>
      <div className="flex justify-end gap-2">
        <Button
          variant="light"
          color="gray"
          onClick={onReset}
          disabled={!dirty || busy}
        >
          Отменить
        </Button>
        <Button onClick={onSave} loading={busy} disabled={!dirty}>
          Сохранить
        </Button>
      </div>
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

// statusDot is the colour of the small dot that leads each server row. It answers
// "is this server serving users", not "is it answering us" — a node whose agent
// syncs every few seconds while its Xray is dead carries nobody, and painting that
// green put a green dot next to the red alert describing the very same server.
//
//	green  — up and serving
//	amber  — on the wire, but its Xray is not running
//	red    — enabled and installed, and we have not heard from it
//	grey   — switched off, or never installed
//
// Exported because the dashboard's fleet strip shows the same servers — two places
// deciding independently what "up" looks like is how they end up disagreeing about
// the same node.
export function statusDot(node: NodeView): string {
  if (!node.enabled || !node.joined) return "bg-gray-400";
  if (!node.is_local && !node.online) return "bg-red-500";
  return node.xray_running ? "bg-emerald-500" : "bg-amber-500";
}

// StatusChip is the small state label next to a server's name. The master needs no
// chip for the states it cannot be in (its name already reads "Мастер" when unnamed);
// plain "up and serving" is left to the green dot to keep the row quiet; the states
// that need words get an xs badge.
function StatusChip({ node }: { node: NodeView }) {
  if (!node.is_local) {
    if (!node.enabled) return <Badge color="gray" size="xs">выключена</Badge>;
    if (!node.joined) return <Badge color="gray" size="xs">не подключена</Badge>;
    if (!node.online) return <Badge color="red" size="xs">офлайн</Badge>;
  }
  // A restart the operator just asked for outranks everything below: during the
  // bounce "Xray не запущен" is true too, and only this says the state is their own
  // click rather than a fault. The outcome is shown for a few seconds after —
  // confirmation lands about a second in, and a badge that appears and vanishes
  // between two refreshes is why the same restart got clicked four times.
  if (node.xray_restart === "pending") {
    return <Badge color="brand" size="xs">перезапуск запрошен</Badge>;
  }
  if (node.xray_restart === "done") {
    return <Badge color="green" size="xs">Xray перезапущен</Badge>;
  }
  if (node.xray_restart === "timeout") {
    return <Badge color="orange" size="xs">перезапуск не подтверждён</Badge>;
  }
  // The amber dot needs a word: reachable but not serving is the one state an
  // operator reads as "fine" if nothing says otherwise.
  if (!node.xray_running) {
    return <Badge color="orange" size="xs">Xray не запущен</Badge>;
  }
  return null; // up and serving → the green dot already says so
}

// serverName is what leads the row: the master shows its configured config-label, or
// "Мастер" when none is set; a node shows its own name. Exported for the dashboard's
// fleet strip, so a renamed master reads the same on both pages.
export function serverName(node: NodeView): string {
  if (node.is_local) return node.master_label?.trim() || "Мастер";
  return node.name;
}

// Sep is the muted middot between inline meta values.
function Sep() {
  return <span className="text-gray-300">·</span>;
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
  // The node is created once; a retry after a failed SSH install reuses this id
  // instead of creating a second orphan node.
  const [createdId, setCreatedId] = useState<number | null>(null);

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
    try {
      // Create the node once; on a retry reuse the existing id so a failed install
      // doesn't leave a trail of orphan "не подключена" nodes.
      let nodeId = createdId;
      if (nodeId == null) {
        setLog(["Создаём ноду…"]);
        const res = await createNode(name.trim(), host.trim());
        nodeId = res.id;
        setCreatedId(res.id);
      } else {
        setLog(["Повторная установка…"]);
      }
      const outcome = await provisionNode(
        nodeId,
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
    <Modal open onClose={onClose} title="Добавить ноду" size="lg" dismissible={!installing}>
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
          <div className="max-h-56 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs">
            {log.map((l, i) => (
              <div key={i} className={l.startsWith("ОШИБКА") ? "text-danger" : ""}>
                {l}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={installing}>
          Отмена
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
  onRegen,
}: {
  node: NodeView;
  onClose: () => void;
  onDone: () => void;
  onRegen: (command: string) => void;
}) {
  // Both tabs reinstall the node; they differ only in who runs the installer. The
  // command tab revokes the node's current token (the old install stops connecting
  // until the command is run), SSH keeps it until the new install succeeds.
  const [mode, setMode] = useState<"command" | "ssh">("command");
  const [busy, setBusy] = useState(false);
  const [sshHost, setSshHost] = useState(node.host);
  const [sshPort, setSshPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [sshAuth, setSshAuth] = useState<"password" | "key">("password");
  const [sshPassword, setSshPassword] = useState("");
  const [sshKey, setSshKey] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [running, setRunning] = useState(false);

  // Command tab: mint a fresh install token and hand the one-liner to the parent,
  // which shows it once (it is a credential — never rendered twice).
  const issueCommand = async () => {
    setBusy(true);
    try {
      const res = await regenNodeJoin(node.id);
      onRegen(res.install_command);
      onClose();
    } catch (e) {
      notifyError(errMessage(e));
      setBusy(false);
    }
  };

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
    <Modal open onClose={onClose} title={`Переустановить «${node.name}»`} size="lg" dismissible={!running}>
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            key={m}
            onClick={() => setMode(m)}
            disabled={running}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onaccent" : "text-ink-muted",
            )}
          >
            {m === "command" ? "Команда установки" : "Переустановить по SSH"}
          </button>
        ))}
      </div>

      {mode === "command" ? (
        <p className="text-sm text-ink-muted">
          Панель выдаст новый токен и команду установки — выполните её на сервере ноды.
          Текущая установка перестанет подключаться сразу, пока вы не выполните команду.
        </p>
      ) : (
        <div className="space-y-3">
          <p className="text-xs text-ink-muted">
            Панель зайдёт на сервер ноды по SSH и переустановит агент с новым токеном.
            Текущая установка продолжает работать, пока новая не встанет. Данные SSH
            нигде не сохраняются.
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
            <div className="max-h-56 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs">
              {log.map((l, i) => (
                <div key={i} className={l.startsWith("ОШИБКА") ? "text-danger" : ""}>
                  {l}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={running}>
          Отмена
        </Button>
        {mode === "command" ? (
          <Button onClick={issueCommand} loading={busy}>
            Получить команду
          </Button>
        ) : (
          <Button onClick={run} loading={running} disabled={!sshHost.trim()}>
            Переустановить
          </Button>
        )}
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
  // base is the last-saved snapshot, for dirty-tracking and "Отменить" (revert).
  // Seeded from init, re-seeded on reset (master's async load), refreshed on commit
  // (after a successful save).
  const [base, setBase] = useState({
    cfg: init.cfg,
    warp: init.warp,
    opera: init.opera,
    country: init.country || "EU",
  });
  const snap = (c: RoutingConfig, w: boolean, o: boolean, cc: string) =>
    JSON.stringify({ c: effectiveCfg(c, laneSources(c.lanes)), w, o, cc });
  const dirty =
    JSON.stringify({ c: effectiveCfg(cfg, laneSrc), w: warpEnabled, o: operaEnabled, cc: operaCountry }) !==
    snap(base.cfg, base.warp, base.opera, base.country);
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
    dirty,
    // revert restores the editor to the last-saved snapshot.
    revert: () => {
      setCfg(base.cfg);
      setLaneSrc(laneSources(base.cfg.lanes));
      setWarpEnabled(base.warp);
      setOperaEnabled(base.opera);
      setOperaCountry(base.country);
    },
    // commit marks the current state as saved (call after a successful save).
    commit: () =>
      setBase({
        cfg: effectiveCfg(cfg, laneSrc),
        warp: warpEnabled,
        opera: operaEnabled,
        country: operaCountry,
      }),
    // reset re-seeds every field AND the baseline (used by the master dialog after its
    // async load, so a freshly-loaded editor is not "dirty").
    reset: (c: RoutingConfig, w: boolean, o: boolean, cc: string) => {
      setCfg(c);
      setLaneSrc(laneSources(c.lanes));
      setWarpEnabled(w);
      setOperaEnabled(o);
      setOperaCountry(cc || "EU");
      setBase({ cfg: c, warp: w, opera: o, country: cc || "EU" });
    },
  };
}

// NodeGeoCard is the node's Geo tab — the same GeoSection as the master (geo file
// status + auto-refresh cadence), but scoped to the node: files come from the node's
// report, "Обновить" queues a refresh on the node, and the cadence is the node's own.
function NodeGeoCard({ node, onChanged }: { node: NodeView; onChanged: () => void }) {
  const [info, setInfo] = useState<GeoInfo | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getNodeGeo(node.id).then(setInfo).catch(() => {});
  }, [node.id]);

  const refresh = async () => {
    setBusy(true);
    try {
      await refreshNodeGeo(node.id);
      notifySuccess("Нода обновит geo при следующей синхронизации");
    } catch (e) {
      notifyError(errMessage(e));
    }
    setBusy(false);
  };

  const changeCadence = async (hours: number) => {
    const prev = info?.refresh_hours ?? node.geo_refresh_hours;
    setInfo((i) => (i ? { ...i, refresh_hours: hours } : i));
    try {
      await setNodeGeoCadence(node.id, hours);
      notifySuccess("Автообновление geo сохранено");
      onChanged();
    } catch (e) {
      // Roll the optimistic update back so the dropdown doesn't misreport the cadence.
      setInfo((i) => (i ? { ...i, refresh_hours: prev } : i));
      notifyError(errMessage(e));
    }
  };

  return (
    <GeoSection
      status={info?.files ?? []}
      onRefresh={refresh}
      refreshing={busy}
      cadence={info?.refresh_hours ?? node.geo_refresh_hours}
      onCadence={changeCadence}
    />
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
  onRefresh,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onClose: () => void;
  onRefresh: () => void;
}) {
  const [name, setName] = useState(node.name);
  const [decoy, setDecoy] = useState(node.decoy_template);
  // genBase / dnsBase are the last-saved snapshots powering dirty-tracking + revert on
  // the Основное and DNS tabs (routing carries its own inside useServerRouting).
  const [genBase, setGenBase] = useState({ name: node.name, decoy: node.decoy_template });
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
  const genDirty = name !== genBase.name || decoy !== genBase.decoy;
  const dnsDirty = dns !== dnsBase;

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

  // Each tab saves on its own (like Подключения/Geo/Домен) and stays open; onRefresh
  // updates the background list. Основное persists name/decoy, Роутинг the routing +
  // egress, DNS its own endpoint — three independent saves.
  const saveGeneral = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await updateNode(node.id, {
        name: name.trim(),
        host: node.host, // domain is changed from the Домен tab
        decoy_template: decoy,
        // Protocols are edited on the Подключения tab; omitting them here tells the
        // panel to preserve the current values (never revert a just-made change).
      });
      setGenBase({ name, decoy });
      notifySuccess("Основное сохранено");
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
      notifySuccess("Роутинг сохранён");
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
      notifySuccess("DNS сохранён");
      onRefresh();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
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
          { value: "connections", label: "Подключения" },
          { value: "routing", label: "Роутинг" },
          { value: "dns", label: "DNS" },
          { value: "geo", label: "Geo" },
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
          <TabSaveBar
            onSave={saveGeneral}
            onReset={() => {
              setName(genBase.name);
              setDecoy(genBase.decoy);
            }}
            dirty={genDirty}
            busy={saving}
          />
        </div>
      )}

      {tab === "connections" && (
        <ConnectionsEditor
          load={() => getNodeConnections(node.id)}
          save={(u) => applyNodeConnections(node.id, u)}
          restartsPanel={false}
        />
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
            iplist={geo.iplist}
            applying={saving}
            liveStatus={false}
          />
          <TabSaveBar onSave={saveRouting} onReset={r.revert} dirty={r.dirty} busy={saving} />
        </div>
      )}

      {tab === "dns" && (
        <div className="flex flex-col gap-4">
          <Section title="DNS" desc="Резолвер, который использует нода. Пусто — по умолчанию.">
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
  onRefresh,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  onClose: () => void;
  onRefresh: () => void;
}) {
  const { applying, apply } = useXrayApply();
  // "Основное" (name + decoy) doesn't touch the Xray config, so it saves without the
  // xray-restart wait that `apply` blocks on — otherwise it hangs polling for a
  // restart that never comes.
  const [savingGeneral, setSavingGeneral] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [name, setName] = useState(node.master_label ?? "");
  const [decoy, setDecoy] = useState(node.decoy_template);
  const [genBase, setGenBase] = useState({
    name: node.master_label ?? "",
    decoy: node.decoy_template,
  });
  const [dns, setDns] = useState(canonicalDns(node.xray_dns ?? ""));
  const [dnsBase, setDnsBase] = useState(canonicalDns(node.xray_dns ?? ""));
  const genDirty = name !== genBase.name || decoy !== genBase.decoy;
  const dnsDirty = dns !== dnsBase;
  // Live egress status for the badges (master's egress runs locally, so the panel
  // knows the real state — unlike a node).
  const [warpRegistered, setWarpRegistered] = useState(node.warp_registered);
  const [operaRunning, setOperaRunning] = useState(false);
  const [operaAlive, setOperaAlive] = useState(false);
  const [proxyCounts, setProxyCounts] = useState<Record<string, number>>({});
  const [geoStatus, setGeoStatus] = useState<GeoFile[]>([]);
  const [ipListStatus, setIPListStatus] = useState<GeoFile[]>([]);
  const [geoCadence, setGeoCadence] = useState(0);
  const [ipListCadence, setIPListCadence] = useState(0);
  const [tab, setTab] = useState("general");
  const r = useServerRouting({
    cfg: EMPTY,
    warp: node.warp_enabled,
    opera: node.opera_enabled,
    country: node.opera_country,
  });
  const reset = r.reset;

  useEffect(() => {
    getGeoStatus()
      .then((g) => {
        setGeoStatus(g.files);
        setIPListStatus(g.iplist_files ?? []);
        setGeoCadence(g.refresh_hours);
        setIPListCadence(g.iplist_refresh_hours ?? 0);
      })
      .catch(() => {});
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const refreshGeo = () =>
    apply(async () => {
      setGeoStatus((await updateGeo()).files);
      notifySuccess("Geo-базы обновлены");
    });

  const refreshIPLists = () =>
    apply(async () => {
      setIPListStatus((await updateIPLists()).iplist_files ?? []);
      notifySuccess("Списки iplist обновлены");
    });

  // Mirrors changeGeoCadence: optimistic, rolled back on failure so the dropdown
  // never misreports the saved schedule.
  const changeIPListCadence = async (hours: number) => {
    const prev = ipListCadence;
    setIPListCadence(hours);
    try {
      await saveIPListCadence(hours);
      notifySuccess("Автообновление списков сохранено");
    } catch (e) {
      setIPListCadence(prev);
      notifyError(errMessage(e));
    }
  };

  const changeGeoCadence = async (hours: number) => {
    setGeoCadence(hours);
    try {
      await saveGeoCadence(hours);
      notifySuccess("Автообновление geo сохранено");
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

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

  // Each tab saves on its own (like Подключения/Geo/Домен) and stays open; onRefresh
  // updates the background list. These map to the panel's global settings behind the
  // master's card, so they stay as separate endpoints.
  const saveGeneral = async () => {
    setSavingGeneral(true);
    try {
      await setMasterName(name.trim());
      await saveDecoy(decoy);
      setGenBase({ name, decoy });
      notifySuccess("Основное сохранено");
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
      notifySuccess("Роутинг сохранён");
      onRefresh();
    });

  const saveDnsTab = () =>
    apply(async () => {
      await setXrayDNS(dns);
      setDnsBase(dns);
      notifySuccess("DNS сохранён");
      onRefresh();
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
              { value: "connections", label: "Подключения" },
              { value: "routing", label: "Роутинг" },
              { value: "dns", label: "DNS" },
              { value: "geo", label: "Geo" },
              { value: "iplist", label: "Списки" },
              { value: "domain", label: "Домен" },
            ]}
          />

          {tab === "general" && (
            <div className="flex flex-col gap-4">
              <Section title="Сервер">
                <TextInput
                  label="Название"
                  value={name}
                  onChange={setName}
                  placeholder="напр. Main (пусто — без префикса)"
                />
                <Select
                  label="Заглушка"
                  value={decoy}
                  onChange={setDecoy}
                  data={decoys.map((d) => ({ value: d, label: DECOY_LABELS[d] ?? d }))}
                />
              </Section>
              <TabSaveBar
                onSave={saveGeneral}
                onReset={() => {
                  setName(genBase.name);
                  setDecoy(genBase.decoy);
                }}
                dirty={genDirty}
                busy={savingGeneral}
              />
            </div>
          )}

          {tab === "connections" && (
            <ConnectionsEditor load={getConnections} save={applyConnections} restartsPanel />
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

          {tab === "dns" && (
            <div className="flex flex-col gap-4">
              <Section title="DNS" desc="Резолвер, который использует Xray. Пусто — по умолчанию.">
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

          {tab === "geo" && (
            <GeoSection
              status={geoStatus}
              onRefresh={refreshGeo}
              refreshing={applying}
              cadence={geoCadence}
              onCadence={changeGeoCadence}
            />
          )}

          {tab === "iplist" && (
            <IPListSection
              status={ipListStatus}
              onRefresh={refreshIPLists}
              refreshing={applying}
              cadence={ipListCadence}
              onCadence={changeIPListCadence}
            />
          )}

          {/* Domain / TLS — its own load + "сменить домен" button (page redirects
              on success), independent of this dialog's Save. */}
          {tab === "domain" && <TLSPanel />}
        </>
      )}
      <ApplyingModal open={applying} />
    </Modal>
  );
}

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
  const [showingConfig, setShowingConfig] = useState(false);
  const [showingHealth, setShowingHealth] = useState(false);
  const [restarting, setRestarting] = useState(false);

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

  const doUpdate = async () => {
    try {
      await updateNodeVersion(node.id);
      notifySuccess("Нода обновляется — Xray перезапустится");
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // Bouncing Xray drops every live connection on THAT server, so it is confirmed
  // first. On the master it happens right away; on a node the panel can only ask —
  // the node acts when its (immediately woken) poll returns, and the row then reads
  // «перезапуск запрошен» until the node reports an Xray that actually restarted.
  // Hence no success toast for a node: the claim isn't ours to make yet.
  const doXrayRestart = async () => {
    const ok = await confirm({
      title: node.is_local
        ? "Перезапустить Xray?"
        : `Перезапустить Xray на «${node.name}»?`,
      body: "Активные VPN-подключения на этом сервере будут разорваны — клиенты переподключатся автоматически через несколько секунд. Конфигурация не изменится.",
      confirmLabel: "Перезапустить",
      danger: true,
    });
    if (!ok) return;
    setRestarting(true);
    try {
      if (node.is_local) {
        await restartXray();
        notifySuccess("Xray перезапущен");
      } else {
        await restartNodeXray(node.id);
        notifySuccess("Ждём подтверждения от ноды");
        onChanged(); // pick up the pending badge now, not on the next poll tick
      }
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRestarting(false);
    }
  };

  return (
    <div className={cn("px-4 py-3.5", !node.enabled && !node.is_local && "opacity-55")}>
      {confirmNode}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", statusDot(node))} />
            <span className="truncate font-semibold text-ink">{serverName(node)}</span>
            {/* Address before the chip: the two identify the server and belong
                together, and a chip wedged between them pushed the address around
                every time the state changed. */}
            <span className="truncate font-mono text-sm text-ink-muted">{node.host}</span>
            <StatusChip node={node} />
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-ink-muted">
            <span>{fmtBytes(node.traffic_up + node.traffic_down)} сегодня</span>
            {!node.is_local && (
              <>
                <Sep />
                <span>{fmtSeen(node.last_seen)}</span>
              </>
            )}
            <Sep />
            <span className={node.version_skew ? "text-amber-600" : undefined}>
              Xray {node.xray_version || "—"}
              {node.version_skew ? " ⚠" : ""}
            </span>
            {!node.is_local && (
              <>
                <Sep />
                <span>агент {node.node_version || "—"}</span>
              </>
            )}
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-1">
          {!node.is_local && <Switch checked={node.enabled} onChange={toggleEnabled} />}
          {/* Four per-server actions, as icons: spelled out they crowded the row and
              pushed the server's own name off a narrow screen. */}
          <IconButton title="Настройки" onClick={() => setEditingRouting(true)}>
            <IconGear size={18} />
          </IconButton>
          <IconButton title="Диагностика" onClick={() => setShowingHealth(true)}>
            <IconPulse size={18} />
          </IconButton>
          <IconButton title="Конфигурация Xray" onClick={() => setShowingConfig(true)}>
            <IconBraces size={18} />
          </IconButton>
          <IconButton title="Логи" onClick={() => setShowingLogs(true)}>
            <IconTerminal size={18} />
          </IconButton>
          <IconButton
            title={
              node.xray_restart === "pending"
                ? "Перезапуск запрошен — ждём подтверждения от ноды"
                : "Перезапустить Xray"
            }
            color="red"
            disabled={
              restarting ||
              node.xray_restart === "pending" ||
              (!node.is_local && (!node.enabled || !node.joined))
            }
            onClick={doXrayRestart}
          >
            <IconRestart
              size={18}
              className={node.xray_restart === "pending" ? "animate-spin" : undefined}
            />
          </IconButton>
          {!node.is_local && (
            <Dropdown
              align="end"
              width={210}
              trigger={
                <span
                  title="Управление нодой"
                  className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-gray-600 transition hover:bg-gray-100 active:scale-90"
                >
                  <IconDots size={18} />
                </span>
              }
            >
              <DropdownItem onClick={doUpdate}>
                Обновить{node.version_skew ? " (новая версия)" : ""}
              </DropdownItem>
              {/* One reinstall action: the dialog offers the command and the SSH way.
                  They were two menu items ("Новый токен" just issued the command for
                  the same reinstall), which read as two different operations. */}
              <DropdownItem onClick={() => setReconnecting(true)}>
                Переустановить
              </DropdownItem>
              <DropdownDivider />
              <DropdownItem color="red" onClick={remove}>
                Удалить
              </DropdownItem>
            </Dropdown>
          )}
        </div>
      </div>
      {reconnecting && (
        <ReconnectDialog
          node={node}
          onClose={() => setReconnecting(false)}
          onRegen={onRegen}
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
            onRefresh={onChanged}
          />
        ) : (
          <NodeSettingsDialog
            node={node}
            decoys={decoys}
            geo={geo}
            onClose={() => setEditingRouting(false)}
            onRefresh={onChanged}
          />
        ))}
      {showingLogs &&
        (node.is_local ? (
          <XrayLogs onClose={() => setShowingLogs(false)} />
        ) : (
          <NodeLogsDialog node={node} onClose={() => setShowingLogs(false)} />
        ))}
      {/* HealthPanel mounts (and starts its light auto-refresh) only while open. */}
      <Modal
        open={showingHealth}
        onClose={() => setShowingHealth(false)}
        title={`Диагностика — «${serverName(node)}»`}
        size="lg"
      >
        <HealthPanel nodeId={node.id} />
      </Modal>
      {showingConfig && (
        <XrayConfigView
          nodeId={node.id}
          title={`Конфигурация Xray — «${node.name}»`}
          note={
            node.is_local
              ? undefined
              : "Конфиг генерирует панель и отдаёт ноде при синхронизации. Пути к сертификатам агент подставляет свои."
          }
          onClose={() => setShowingConfig(false)}
        />
      )}
    </div>
  );
}

// classifyNodeLog buckets a node log line by level. Node logs mix the agent's slog
// output ([INFO]/[WARN]/[ERROR]) with the Xray tail ([error]/[warning]/accepted),
// so this recognises both (case-insensitive).
function classifyNodeLog(l: string): string {
  if (/\[error\]|\bpanic\b|\bfatal\b|failed|rejected/i.test(l)) return "error";
  if (/\[warn(ing)?\]/i.test(l)) return "warning";
  if (/accepted/i.test(l)) return "access";
  if (/\[info\]/i.test(l)) return "info";
  return "other";
}

// Theme-aware level colours matching the dashboard's Xray log viewer (they adapt to
// the surface luminance, so they read on the light-on-dark `bg-gray-50` surface).
const NODE_LOG_COLORS: Record<string, string> = {
  error: "text-danger",
  warning: "text-warning",
  access: "text-success",
  info: "text-brand-600",
};

const NODE_LOG_FILTERS = [
  { value: "all", label: "Все" },
  { value: "access", label: "Доступ" },
  { value: "info", label: "Инфо" },
  { value: "warning", label: "Предупр." },
  { value: "error", label: "Ошибки" },
];

// NodeLogsDialog streams a node's recent logs. It polls the panel, which asks the
// node to include its log tail on its next sync (agent + Xray), so the view stays
// fresh while open (with up to one sync interval of latency). Tabs filter by level.
function NodeLogsDialog({ node, onClose }: { node: NodeView; onClose: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [level, setLevel] = useState("all");

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

  const shown =
    level === "all"
      ? lines
      : lines.filter((l) => classifyNodeLog(l) === level);

  return (
    <Modal open onClose={onClose} title={`Логи — «${node.name}»`} size="xl">
      <div className="mb-3 overflow-x-auto">
        <SegmentedControl data={NODE_LOG_FILTERS} value={level} onChange={setLevel} />
      </div>
      {!loaded ? (
        <p className="text-sm text-ink-muted">Запрашиваем логи у ноды…</p>
      ) : lines.length === 0 ? (
        <p className="text-sm text-ink-muted">
          Логи пока не получены. Нода пришлёт их при следующей синхронизации.
        </p>
      ) : (
        <div className="max-h-[60vh] overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs leading-relaxed">
          {shown.length === 0 ? (
            <p className="text-gray-400">Нет строк выбранного уровня</p>
          ) : (
            shown.map((l, i) => (
              <div
                key={i}
                className={cn(
                  "whitespace-pre-wrap break-all",
                  NODE_LOG_COLORS[classifyNodeLog(l)],
                )}
              >
                {l}
              </div>
            ))
          )}
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

export function NodesPanel() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
  // Geo categories feed the routing editor's domain/IP suggestions (same list for
  // the master and every node — one panel-side geosite/geoip).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [], iplist: [] });
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
  // перезапущен" badge on screen until the next lazy tick, well past the few
  // seconds the server means it to be shown. Otherwise this is just the liveness
  // refresh keeping online/offline badges current, and it stays lazy.
  const showingRestart = !!nodes?.some((n) => n.xray_restart);
  useEffect(() => {
    const t = setInterval(load, showingRestart ? 2000 : 15000);
    return () => clearInterval(t);
  }, [showingRestart]);

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
        </div>
        <div className="flex flex-wrap gap-2">
          {remoteCount > 0 && (
            <Button variant="light" color="gray" onClick={updateAll}>
              Обновить все ноды{anyStale ? " ⚠" : ""}
            </Button>
          )}
          <Button onClick={() => setAdding(true)}>Добавить ноду</Button>
        </div>
      </div>

      <Card className="divide-y divide-gray-100">
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
      </Card>

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
