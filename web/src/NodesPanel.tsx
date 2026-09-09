import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExternalServers } from "./ExternalServers";
import i18n from "./i18n";
import {
  applyConnections,
  applyNodeConnections,
  resetConnections,
  resetNodeConnections,
  createNode,
  deleteNode,
  getConnections,
  getGeoCategories,
  getMe,
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
  setMasterPlacement,
  type Placement,
  setNodeDNS,
  setServerProxy,
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
  type SystemProxy,
  type SystemProxyAccount,
  type RoutingConfig,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { ConnectionsEditor } from "./ConnectionsEditor";
import { InboundsEditor } from "./InboundsEditor";
import { ServerSnapshots } from "./ServerSnapshots";
import { canonicalDns, DnsEditor } from "./DnsEditor";
import { helperStatus } from "./egress";
import { fmtBytes, fmtStamp } from "./format";
import { decoyLabel } from "./GeneralSettings";
import { HealthPanel } from "./HealthPanel";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { EMPTY_STEP_UP, type StepUp, StepUpFields, stepUpReady, useTotpEnabled } from "./stepup";
import { TLSPanel } from "./TLSPanel";
import { XrayConfigView } from "./XrayConfig";
import { XrayLogs } from "./XrayLogs";
import {
  effectiveCfg,
  EMPTY,
  GeoSection,
  hydrateRouting,
  IPListSection,
  laneSources,
  RoutingEditor,
  type LaneSource,
  type StatusBadge,
} from "./RoutingEditor";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  Code,
  Drawer,
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
  IconTrash,
  MiniBar,
  Modal,
  Mono,
  PasswordInput,
  Section,
  SegmentedControl,
  Select,
  SettingRow,
  Switch,
  Textarea,
  TextInput,
  type Tone,
  ToolDialog,
  useConfirm,
} from "./ui";
import { PlacementFields, placementOf } from "./PlacementFields";

// DialogTabs is the in-modal tab strip used by the server settings dialogs, so a
// server's many sections (domain / routing / DNS / …) don't stack into one long
// scroll. All tabs' state lives in the parent, so switching never loses edits and
// the single footer Save persists everything regardless of the active tab.
// ERR_PREFIX marks a failed line in the live install log so it can be coloured
// without parsing the message itself.
const ERR_PREFIX = "ERROR";

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
    <div className="no-scrollbar -mx-5 flex gap-0.5 overflow-x-auto px-5">
      {tabs.map((t) => (
        <button
          type="button"
          key={t.value}
          onClick={() => onChange(t.value)}
          className={cn(
            "whitespace-nowrap border-b-2 px-3 py-2.5 text-[13px] font-semibold transition",
            value === t.value
              ? "border-brand-600 text-ink"
              : "border-transparent text-ink-muted hover:text-ink",
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

// TabSaveBar is the inline save row for the "form" tabs (general / routing / DNS)
// of the server dialogs, so each tab commits on its own — exactly like the
// connections / geo / domain tabs already do, staying open after save. Cancel
// reverts unsaved edits to the last-saved state; both buttons disable when there's
// nothing to save. The note spells out that edits are staged and per-section.
function TabSaveBar({
  onSave,
  onReset,
  dirty,
  busy,
  invalid,
}: {
  onSave: () => void;
  onReset: () => void;
  dirty: boolean;
  busy: boolean;
  // invalid blocks Save only. Cancel stays live on purpose: a form the panel refuses
  // to save is exactly when the operator most needs a way back to the saved state.
  invalid?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-2 border-t border-gray-200 pt-4 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-ink-muted">
        {t("conn.saveHint")}
      </p>
      <div className="flex justify-end gap-2">
        <Button
          variant="light"
          color="gray"
          onClick={onReset}
          disabled={!dirty || busy}
        >
          {t("common.cancel")}
        </Button>
        <Button onClick={onSave} loading={busy} disabled={!dirty || invalid}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}

// systemProxyIssue names why a proxy draft cannot be saved, or "" when it can. The
// same rules the server enforces (model.SystemProxy.Validate) — checked here too so
// the operator sees the reason next to the field instead of a toast after a round
// trip, and so Save is not offered for a state that will bounce.
function systemProxyIssue(p: SystemProxy): string {
  if (!p.socks_enabled && !p.http_enabled) return "";
  const accounts = p.accounts ?? [];
  if (accounts.length === 0) return i18n.t("err.proxyNeedsAccount");
  const seen = new Set<string>();
  for (const a of accounts) {
    const user = a.user.trim();
    if (!user) return i18n.t("err.proxyAccountNoUser");
    if (!a.pass.trim()) return i18n.t("err.proxyAccountNoPass", { value: user });
    if (/[: ]/.test(user)) return i18n.t("err.proxyUserCharset");
    if (seen.has(user)) return i18n.t("err.proxyUserDuplicate", { value: user });
    seen.add(user);
  }
  if (p.socks_enabled && p.http_enabled && p.socks_port === p.http_port) {
    return i18n.t("err.proxyPortsCollide");
  }
  return "";
}

// randomProxyPass mints a password for a first-time enable, so the operator never
// has to invent one (and never leaves the field to whatever they type twice).
function randomProxyPass(): string {
  const bytes = new Uint8Array(18);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes))
    .replace(/[+/=]/g, "")
    .slice(0, 20);
}

// SystemProxyEditor is one server's SOCKS/HTTP forward proxy — the same panel for
// the master and for a node, because the listener is the same thing on both. These
// proxies are NOT part of the VPN surface: no user's credential opens them, no access
// group gates them, they never appear in a subscription. They exist so something that
// isn't a VPN client can go out through this server.
//
// Rendered INSIDE the General tab's server card, not as a card of its own: it is one
// switch and a port per protocol, and a separate panel with a separate save button
// read as a second, unrelated screen. The draft and the last-saved copy belong to the
// tab, so the whole tab still has exactly one save.
function SystemProxyEditor({
  host,
  value,
  saved,
  onChange,
}: {
  host: string;
  value: SystemProxy;
  saved: SystemProxy; // last-saved copy: what the ready-to-paste addresses describe
  onChange: (p: SystemProxy) => void;
}) {
  const { t } = useTranslation();
  const cur = value;
  const base = saved;
  const patch = (p: Partial<SystemProxy>) => onChange({ ...cur, ...p });
  const on = cur.socks_enabled || cur.http_enabled;

  const accounts = cur.accounts ?? [];

  // Turning a protocol on for the first time fills in the port and mints an account:
  // an enable that then refuses to save because there is nobody to authenticate is a
  // worse first experience than one that just works and can be edited.
  const enable = (key: "socks_enabled" | "http_enabled", v: boolean) => {
    const next: Partial<SystemProxy> = { [key]: v } as Partial<SystemProxy>;
    if (v) {
      if (key === "socks_enabled" && !cur.socks_port) next.socks_port = 1080;
      if (key === "http_enabled" && !cur.http_port) next.http_port = 3128;
      if (accounts.length === 0) next.accounts = [{ user: "proxy", pass: randomProxyPass() }];
    }
    patch(next);
  };

  const setAccount = (i: number, a: Partial<SystemProxyAccount>) =>
    patch({ accounts: accounts.map((old, j) => (j === i ? { ...old, ...a } : old)) });
  // The suggested login is the first free proxyN, not "count + 1": deleting the
  // first row and adding one would otherwise propose a name already in the list, and
  // the save would come back with "the login is used twice".
  const addAccount = () => {
    const taken = new Set(accounts.map((a) => a.user));
    let n = accounts.length + 1;
    while (taken.has(`proxy${n}`)) n++;
    patch({ accounts: [...accounts, { user: `proxy${n}`, pass: randomProxyPass() }] });
  };
  const removeAccount = (i: number) =>
    patch({ accounts: accounts.filter((_, j) => j !== i) });

  // savedAccount is the stored twin of a draft row, matched on the exact credentials:
  // an address is only truthful for a login the server has actually been given, so a
  // row that is still being typed shows none.
  const savedAccount = (i: number): SystemProxyAccount | undefined =>
    (base.accounts ?? []).find(
      (a) => a.user === accounts[i]?.user && a.pass === accounts[i]?.pass,
    );
  const url = (scheme: string, enabled: boolean, port: number, a: SystemProxyAccount) =>
    enabled && host && port
      ? `${scheme}://${encodeURIComponent(a.user)}:${encodeURIComponent(a.pass)}@${host}:${port}`
      : "";

  return (
    <Section
      title={t("proxy.title")}
      desc={t("proxy.hint")}
      action={
        on ? (
          <Button size="xs" variant="light" onClick={addAccount}>
            {t("proxy.addAccount")}
          </Button>
        ) : undefined
      }
      flush
    >
      <ProxyListenerRow
        label="SOCKS5"
        enabled={cur.socks_enabled}
        port={cur.socks_port}
        defaultPort={1080}
        onToggle={(v) => enable("socks_enabled", v)}
        onPort={(v) => patch({ socks_port: v })}
      />
      <ProxyListenerRow
        label="HTTP"
        enabled={cur.http_enabled}
        port={cur.http_port}
        defaultPort={3128}
        onToggle={(v) => enable("http_enabled", v)}
        onPort={(v) => patch({ http_port: v })}
      />

      {/* The accounts appear once something is listening: with both protocols off
          there is nobody to authenticate, and empty rows would just be noise. Each
          account is its own row so one consumer can be revoked without touching the
          others — which is the whole reason there is a list rather than one login. */}
      {on && accounts.length === 0 && (
        <SettingRow hint={t("proxy.noAccounts")} />
      )}
      {on &&
        accounts.map((a, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: the account list is the wire payload — it carries no id, and its position is what every mutator here addresses
          <SettingRow key={i}>
            <div className="flex flex-col gap-2">
              {/* Login, password and the delete control on ONE line: the button
                  belongs to this account, and on its own row it read as an action on
                  the whole list. */}
              <div className="flex items-end gap-2">
                <div className="min-w-0 flex-1">
                  <TextInput
                    label={t("proxy.user")}
                    value={a.user}
                    onChange={(v) => setAccount(i, { user: v })}
                  />
                </div>
                <div className="min-w-0 flex-1">
                  <TextInput
                    label={t("proxy.pass")}
                    mono
                    value={a.pass}
                    onChange={(v) => setAccount(i, { pass: v })}
                  />
                </div>
                <IconButton
                  color="red"
                  title={t("common.delete")}
                  onClick={() => removeAccount(i)}
                >
                  <IconTrash />
                </IconButton>
              </div>
              {/* The addresses come from the SAVED copy: a URL built from a port that
                  is still only typed into the form points at nothing. */}
              {savedAccount(i) && (
                <div className="flex flex-col gap-1.5">
                  {url("socks5", base.socks_enabled, base.socks_port, savedAccount(i)!) && (
                    <Code block copy>
                      {url("socks5", base.socks_enabled, base.socks_port, savedAccount(i)!)}
                    </Code>
                  )}
                  {url("http", base.http_enabled, base.http_port, savedAccount(i)!) && (
                    <Code block copy>
                      {url("http", base.http_enabled, base.http_port, savedAccount(i)!)}
                    </Code>
                  )}
                </div>
              )}
            </div>
          </SettingRow>
        ))}
    </Section>
  );
}

// ProxyListenerRow is one protocol of the system proxy. The port input carries no
// label of its own — a floating "Port" caption above a switch row is what made this
// block look like a pile of unrelated fields — and it is width-boxed by a wrapper
// because the shared input is w-full by design.
function ProxyListenerRow({
  label,
  enabled,
  port,
  defaultPort,
  onToggle,
  onPort,
}: {
  label: string;
  enabled: boolean;
  port: number;
  defaultPort: number;
  onToggle: (v: boolean) => void;
  onPort: (v: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <SettingRow
      label={label}
      hint={!enabled ? t("conn.off") : undefined}
      control={
        <div className="flex items-center gap-3">
          <span className="text-[11px] text-ink-muted">{t("conn.port")}</span>
          <div className="w-24">
            {/* A listener that was never given a port shows the one it will get when
                switched on, as a value rather than a placeholder: next to a row whose
                port was saved, a grey hint reads as a different kind of number. */}
            <TextInput
              type="number"
              value={port ? String(port) : enabled ? "" : String(defaultPort)}
              onChange={(v) => onPort(Number(v) || 0)}
              placeholder={String(defaultPort)}
              disabled={!enabled}
            />
          </div>
          <Switch checked={enabled} onChange={onToggle} />
        </div>
      }
    />
  );
}

function fmtSeen(unix: number): string {
  if (!unix) return i18n.t("nodes.neverJoined");
  const ago = Math.floor(Date.now() / 1000) - unix;
  if (ago < 60) return i18n.t("lastSeen.justNow");
  if (ago < 3600) return i18n.t("lastSeen.minutes", { n: Math.floor(ago / 60) });
  if (ago < 86400) return i18n.t("lastSeen.hours", { n: Math.floor(ago / 3600) });
  return fmtStamp(unix);
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
// clampCoefficient parses the traffic-multiplier field and holds it in the range the
// server accepts (0.1–10), defaulting a blank or unparseable value to the neutral 1.
function clampCoefficient(v: string): number {
  const n = parseFloat(v);
  if (!isFinite(n) || n <= 0) return 1;
  return Math.min(Math.max(n, 0.1), 10);
}

// NodeState is a server's steady state in one place: the dot's colour, the word for
// it and how that word should read. The dashboard writes it as text in a dense row
// and the server card as a badge; before this each spelled the rules out again and
// the two could drift apart on a state neither had thought about.
//
// Deliberately NOT covering the transient restart states (see RestartChip): those are
// about a click the operator just made, not about how the server is.
export type NodeState = {
  dot: string; // background class for the state dot
  label: string;
  tone: Tone;
};

export function nodeState(node: NodeView): NodeState {
  if (!node.is_local && !node.enabled) {
    return { dot: "bg-gray-400", label: i18n.t("nodes.disabled"), tone: "default" };
  }
  if (!node.is_local && !node.joined) {
    return { dot: "bg-gray-400", label: i18n.t("nodes.notJoined"), tone: "default" };
  }
  if (!node.is_local && !node.online) {
    return { dot: "bg-danger", label: i18n.t("usersPanel.offline"), tone: "danger" };
  }
  if (!node.xray_running) {
    return { dot: "bg-warning", label: i18n.t("nodes.xrayDown"), tone: "warning" };
  }
  if (!node.is_local && node.sync_fails >= UNSTABLE_SYNC_FAILS) {
    return { dot: "bg-warning", label: i18n.t("nodes.unstable"), tone: "warning" };
  }
  return { dot: "bg-success", label: i18n.t("nodes.serving"), tone: "success" };
}

// serving counts the servers actually carrying traffic — reachable AND running Xray.
export function servingCount(nodes: NodeView[]): number {
  return nodes.filter(
    (n) => n.enabled && n.joined && (n.is_local || n.online) && n.xray_running,
  ).length;
}

export function statusDot(node: NodeView): string {
  return nodeState(node).dot;
}


// RestartChip is the outcome of a restart the operator just asked for. It outranks
// the steady state in the line below: during the bounce "Xray not running" is true
// too, and only this says the state is their own click rather than a fault. The
// outcome shows for a few seconds after — confirmation lands about a second in, and
// a badge that appears and vanishes between two refreshes is why the same restart
// got clicked four times.
function RestartChip({ node }: { node: NodeView }) {
  if (node.xray_restart === "pending") {
    return <Badge color="brand" size="xs">{i18n.t("nodes.restartQueued")}</Badge>;
  }
  if (node.xray_restart === "done") {
    return <Badge color="green" size="xs">{i18n.t("nodes.xrayRestarted")}</Badge>;
  }
  if (node.xray_restart === "timeout") {
    return <Badge color="orange" size="xs">{i18n.t("nodes.restartUnconfirmed")}</Badge>;
  }
  return null;
}

// UNSTABLE_SYNC_FAILS is how many dropped syncs in the last hour a node reports before
// it's flagged unstable — above the odd blip, below a genuinely limping transport.
const UNSTABLE_SYNC_FAILS = 6;

// serverName is what leads the row: the master shows its configured config-label, or
// "Master" when none is set; a node shows its own name. Exported for the dashboard's
// fleet strip, so a renamed master reads the same on both pages.
export function serverName(node: NodeView): string {
  if (node.is_local) return node.master_label?.trim() || i18n.t("nodes.master");
  return node.name;
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
  const { t } = useTranslation();
  return (
    <Modal open onClose={onClose} title={t("nodes.installCommand")} size="lg">
      <p className="text-sm text-ink-muted">
        {t("nodes.installCommandHint")}
      </p>
      <div className="mt-3">
        <Code block copy>
          {command}
        </Code>
      </div>
      <div className="mt-4 flex justify-end">
        <Button onClick={onClose}>{t("common.done")}</Button>
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
  const { t } = useTranslation();
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
      // doesn't leave a trail of orphan not-joined nodes.
      let nodeId = createdId;
      if (nodeId == null) {
        setLog([t("nodes.creating")]);
        const res = await createNode(name.trim(), host.trim());
        nodeId = res.id;
        setCreatedId(res.id);
      } else {
        setLog([t("nodes.reinstalling")]);
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
        notifySuccess(t("nodes.installedOverSsh"));
        onDone();
      } else {
        notifyError(t("nodes.installFailed"));
        setInstalling(false);
      }
    } catch (e) {
      setLog((l) => [...l, `${ERR_PREFIX}: ${errMessage(e)}`]);
      notifyError(errMessage(e));
      setInstalling(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("nodes.addNode")}
      size="lg"
      dismissible={!installing}
    >
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            type="button"
            key={m}
            onClick={() => setMode(m)}
            disabled={installing}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onaccent" : "text-ink-muted",
            )}
          >
            {t(m === "command" ? "nodes.tabCommand" : "nodes.tabSsh")}
          </button>
        ))}
      </div>

      <div className="space-y-3">
        <TextInput label={t("groups.name")} value={name} onChange={setName} placeholder={t("nodes.namePlaceholder")} />
        <TextInput
          label={t("nodes.hostLabel")}
          value={host}
          onChange={setHost}
          placeholder="nl1.example.com"
        />

        {mode === "ssh" && (
          <div className="space-y-3 border-t border-gray-100 pt-3">
            <p className="text-xs text-ink-muted">
              {t("nodes.sshHint")}
            </p>
            <div className="grid grid-cols-3 gap-2">
              <div className="col-span-2">
                <TextInput label={t("nodes.sshHost")} value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
              </div>
              <TextInput label={t("conn.port")} value={sshPort} onChange={setSshPort} placeholder="22" />
            </div>
            <TextInput label={t("nodes.sshUser")} value={sshUser} onChange={setSshUser} placeholder="root" />
            <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
              {(["password", "key"] as const).map((a) => (
                <button
                  type="button"
                  key={a}
                  onClick={() => setSshAuth(a)}
                  className={cn(
                    "rounded-md px-3 py-1 transition",
                    sshAuth === a ? "bg-brand-600 text-onaccent" : "text-ink-muted",
                  )}
                >
                  {t(a === "password" ? "login.password" : "nodes.key")}
                </button>
              ))}
            </div>
            {sshAuth === "password" ? (
              <PasswordInput label={t("nodes.sshPassword")} value={sshPassword} onChange={setSshPassword} />
            ) : (
              <Textarea
                label={t("nodes.privateKey")}
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
              // biome-ignore lint/suspicious/noArrayIndexKey: an install transcript is positional — lines repeat verbatim and only ever append
              <div key={i} className={l.startsWith(ERR_PREFIX) ? "text-danger" : ""}>
                {l}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={installing}>
          {t("common.cancel")}
        </Button>
        {mode === "command" ? (
          <Button onClick={submitCommand} loading={busy} disabled={!name.trim() || !host.trim()}>
            {t("common.create")}
          </Button>
        ) : (
          <Button
            onClick={submitSSH}
            loading={installing}
            disabled={!name.trim() || !host.trim() || !sshHost.trim()}
          >
            {t("nodes.install")}
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
  const { t } = useTranslation();
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
    setLog([t("nodes.reinstallingNode")]);
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
        notifySuccess(t("nodes.reinstalled"));
        onDone();
      } else {
        notifyError(t("nodes.failedSeeLog"));
        setRunning(false);
      }
    } catch (e) {
      setLog((l) => [...l, `${ERR_PREFIX}: ${errMessage(e)}`]);
      notifyError(errMessage(e));
      setRunning(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("nodes.reinstallOf", { name: node.name })}
      size="lg"
      dismissible={!running}
    >
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            type="button"
            key={m}
            onClick={() => setMode(m)}
            disabled={running}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onaccent" : "text-ink-muted",
            )}
          >
            {t(m === "command" ? "nodes.tabCommand" : "nodes.tabReinstallSsh")}
          </button>
        ))}
      </div>

      {mode === "command" ? (
        <p className="text-sm text-ink-muted">
          {t("nodes.reinstallCommandHint")}
        </p>
      ) : (
        <div className="space-y-3">
          <p className="text-xs text-ink-muted">
            {t("nodes.reinstallSshHint")}
          </p>
          <div className="grid grid-cols-3 gap-2">
            <div className="col-span-2">
              <TextInput label={t("nodes.sshHost")} value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
            </div>
            <TextInput label={t("conn.port")} value={sshPort} onChange={setSshPort} placeholder="22" />
          </div>
          <TextInput label={t("nodes.sshUser")} value={sshUser} onChange={setSshUser} placeholder="root" />
          <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
            {(["password", "key"] as const).map((a) => (
              <button
                type="button"
                key={a}
                onClick={() => setSshAuth(a)}
                className={cn(
                  "rounded-md px-3 py-1 transition",
                  sshAuth === a ? "bg-brand-600 text-onaccent" : "text-ink-muted",
                )}
              >
                {t(a === "password" ? "login.password" : "nodes.key")}
              </button>
            ))}
          </div>
          {sshAuth === "password" ? (
            <PasswordInput label={t("nodes.sshPassword")} value={sshPassword} onChange={setSshPassword} />
          ) : (
            <Textarea
              label={t("nodes.privateKey")}
              value={sshKey}
              onChange={setSshKey}
              rows={4}
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            />
          )}
          {log.length > 0 && (
            <div className="max-h-56 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs">
              {log.map((l, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: an install transcript is positional — lines repeat verbatim and only ever append
                <div key={i} className={l.startsWith(ERR_PREFIX) ? "text-danger" : ""}>
                  {l}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={running}>
          {t("common.cancel")}
        </Button>
        {mode === "command" ? (
          <Button onClick={issueCommand} loading={busy}>
            {t("nodes.getCommand")}
          </Button>
        ) : (
          <Button onClick={run} loading={running} disabled={!sshHost.trim()}>
            {t("nodes.reinstall")}
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
  // base is the last-saved snapshot, for dirty-tracking and Cancel (revert).
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
    // What the server currently has, as against the draft above: a status word must
    // describe the running egress, not a switch nobody has saved yet.
    savedWarp: base.warp,
    savedOpera: base.opera,
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
// report, the refresh button queues a refresh on the node, and the cadence is the node's own.
function NodeGeoCard({ node, onChanged }: { node: NodeView; onChanged: () => void }) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<GeoInfo | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getNodeGeo(node.id).then(setInfo).catch(() => {});
  }, [node.id]);

  const refresh = async () => {
    setBusy(true);
    try {
      await refreshNodeGeo(node.id);
      notifySuccess(t("nodes.geoQueued"));
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
      notifySuccess(t("nodes.geoCadenceSaved"));
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

// MasterNameEditor lets the operator name the master server for config labels
// (shown as "<name> · VLESS…" in clients). Empty = no prefix.
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
  const genDirty = name !== genBase.name || decoy !== genBase.decoy || proxyDirty || plDirty;
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
      await setMasterName(name.trim());
      if (plDirty) {
        await setMasterPlacement(pl);
        setPlBase(pl);
      }
      await saveDecoy(decoy);
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
            value={tab}
            onChange={setTab}
            tabs={[
              { value: "general", label: t("settings.tabGeneral") },
              { value: "connections", label: t("nodes.tabConnections") },
              { value: "inbounds", label: t("nodes.tabInbounds") },
              { value: "routing", label: t("nodes.tabRouting") },
              { value: "dns", label: "DNS" },
              { value: "geo", label: "Geo" },
              { value: "iplist", label: t("nodes.tabLists") },
              { value: "domain", label: t("restore.domain") },
              { value: "snapshots", label: t("nodes.tabSnapshots") },
            ]}
          />
        ) : undefined
      }
    >
      {!loaded ? (
        <CenterLoader />
      ) : (
        <>

          {tab === "general" && (
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
                  setPl(plBase);
                  setProxy(proxyBase);
                }}
                dirty={genDirty}
                busy={savingGeneral}
                invalid={proxyIssue !== ""}
              />
            </div>
          )}

          {tab === "connections" && (
            <ConnectionsEditor load={getConnections} save={applyConnections} reset={resetConnections} restartsPanel />
          )}

          {tab === "inbounds" && <InboundsEditor serverId={0} restartsPanel />}

          {tab === "routing" && (
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

          {tab === "dns" && (
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

          {/* Domain / TLS — its own load + change-domain button (page redirects
              on success), independent of this dialog's Save. */}
          {tab === "domain" && <TLSPanel />}

          {tab === "snapshots" && (
            <ServerSnapshots
              onRolledBack={() => {
                onRefresh();
                onClose();
              }}
            />
          )}
        </>
      )}
      <ApplyingModal open={applying} />
    </Drawer>
  );
}

// cmpVersion orders two release strings the way semver does: the numbers first,
// left to right, then the pre-release suffix — "2.14.0-rc1" comes before the
// "2.14.0" it precedes, and a part that is missing counts as zero.
function cmpVersion(a: string, b: string): number {
  const parse = (v: string) => {
    const [core, ...pre] = v.replace(/^v/, "").split("-");
    return { nums: core.split(".").map((n) => parseInt(n, 10) || 0), pre: pre.join("-") };
  };
  const x = parse(a);
  const y = parse(b);
  for (let i = 0; i < Math.max(x.nums.length, y.nums.length); i++) {
    const d = (x.nums[i] ?? 0) - (y.nums[i] ?? 0);
    if (d !== 0) return d;
  }
  if (x.pre === y.pre) return 0;
  if (!x.pre) return 1;
  if (!y.pre) return -1;
  return x.pre < y.pre ? -1 : 1;
}

// NodeCard renders one node with its status, traffic, protocol toggles and decoy.
// agentSkew says which way this node's agent build differs from the panel's own
// version: "older" is the one "Update all" fixes, "newer" happens when a server was
// updated by hand ahead of the panel and it is the panel that is behind. Not the
// same question as NodeView.version_skew, which is about the Xray build the panel
// pins — a node can be current on one and behind on the other.
type AgentSkew = "" | "older" | "newer";

function agentSkew(node: NodeView, panelVersion: string): AgentSkew {
  if (node.is_local || !node.node_version || !panelVersion) return "";
  const d = cmpVersion(node.node_version, panelVersion);
  return d < 0 ? "older" : d > 0 ? "newer" : "";
}

function NodeCard({
  node,
  decoys,
  geo,
  panelVersion,
  onChanged,
  onRegen,
}: {
  node: NodeView;
  decoys: string[];
  geo: GeoCategories;
  panelVersion: string;
  onChanged: () => void;
  onRegen: (command: string) => void;
}) {
  const { t } = useTranslation();
  const { confirm, confirmNode } = useConfirm();
  const [reconnecting, setReconnecting] = useState(false);
  const [editingRouting, setEditingRouting] = useState(false);
  const [showingLogs, setShowingLogs] = useState(false);
  const [showingConfig, setShowingConfig] = useState(false);
  const [showingHealth, setShowingHealth] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [removeCreds, setRemoveCreds] = useState<StepUp>(EMPTY_STEP_UP);
  const [removing, setRemoving] = useState(false);
  const totpEnabled = useTotpEnabled();

  const toggleEnabled = async (enabled: boolean) => {
    try {
      await setNodeEnabled(node.id, enabled);
      onChanged();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // Deleting a server is re-authorised, not just confirmed: it cuts off everyone on
  // that node at once and the panel has no undo for it — the box has to be installed
  // and joined again. Hence a form rather than the shared yes/no dialog.
  const closeRemove = () => {
    setRemoveOpen(false);
    setRemoveCreds(EMPTY_STEP_UP);
  };

  const remove = async () => {
    setRemoving(true);
    try {
      await deleteNode(node.id, removeCreds.password, removeCreds.code);
      closeRemove();
      notifySuccess(t("nodes.deleted"));
      onChanged();
    } catch (e) {
      // The dialog stays open: a refused code is the common case, and it is worth
      // exactly one more attempt rather than a re-opened form with the password gone.
      notifyError(errMessage(e));
    } finally {
      setRemoving(false);
    }
  };

  const removeModal = (
    <Modal open={removeOpen} onClose={closeRemove} title={t("nodes.deleteTitle")}>
      <p className="text-sm leading-relaxed text-ink-muted">
        {t("nodes.deleteBody", { name: node.name })}
      </p>
      {/* withCode: deleting a node goes through verifyStepUpTOTP on the server. */}
      <StepUpFields value={removeCreds} onChange={setRemoveCreds} withCode />
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={closeRemove}>
          {t("common.cancel")}
        </Button>
        <Button
          color="red"
          loading={removing}
          disabled={!stepUpReady(removeCreds, totpEnabled)}
          onClick={remove}
        >
          {t("common.delete")}
        </Button>
      </div>
    </Modal>
  );

  const doUpdate = async () => {
    try {
      await updateNodeVersion(node.id);
      notifySuccess(t("nodes.updating"));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  // Bouncing Xray drops every live connection on THAT server, so it is confirmed
  // first. On the master it happens right away; on a node the panel can only ask —
  // the node acts when its (immediately woken) poll returns, and the row then reads
  // the restart-queued badge until the node reports an Xray that actually restarted.
  // Hence no success toast for a node: the claim isn't ours to make yet.
  const doXrayRestart = async () => {
    const ok = await confirm({
      title: node.is_local
        ? t("nodes.restartXrayTitle")
        : t("nodes.restartXrayOn", { name: node.name }),
      body: t("nodes.restartXrayBody"),
      confirmLabel: t("manage.restartConfirm"),
      danger: true,
    });
    if (!ok) return;
    setRestarting(true);
    try {
      if (node.is_local) {
        await restartXray();
        notifySuccess(t("nodes.xrayRestarted"));
      } else {
        await restartNodeXray(node.id);
        notifySuccess(t("nodes.awaitingNode"));
        onChanged(); // pick up the pending badge now, not on the next poll tick
      }
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRestarting(false);
    }
  };

  const state = nodeState(node);
  const pct = (used: number, total: number) => (total > 0 ? (used / total) * 100 : 0);
  const traffic = node.traffic_up + node.traffic_down;
  // The line under the address: whether we are hearing from it, and what it runs.
  // The master answers for itself, so it has no "last seen" to report.
  const version = node.is_local ? panelVersion : node.node_version;
  const skew = agentSkew(node, panelVersion);
  // The line under the address: how the server is, and when we last heard it say so.
  // The master answers for itself — there is no "last seen" for the machine you are
  // talking to, and its Xray version is a click away in the config it serves.
  const sublineTail = node.is_local ? "" : fmtSeen(node.last_seen);

  return (
    <section
      className={cn(
        "rounded-xl border border-brand-600/10 bg-white",
        !node.enabled && !node.is_local && "opacity-55",
      )}
    >
      {confirmNode}
      {removeModal}

      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-brand-600/10 px-3.5 py-2.5">
        <span className={cn("size-2 shrink-0 rounded-full", state.dot)} />
        <span className="shrink truncate text-sm font-semibold text-ink">{serverName(node)}</span>
        {node.is_local && !!node.master_label?.trim() && (
          <Badge size="xs" color="brand" className="shrink-0">
            {t("nodes.master")}
          </Badge>
        )}
        {/* What this server runs, plainly: the number, not the word "agent" in front
            of it. The master answers with the panel's own version — it IS the panel. */}
        {!!version && (
          <Badge size="xs" color="gray" className="shrink-0">
            <span className="font-mono">{version}</span>
          </Badge>
        )}
        <RestartChip node={node} />
        {skew && (
          <Badge
            size="xs"
            color={skew === "older" ? "orange" : "gray"}
            className="shrink-0 max-sm:hidden"
          >
            {t(skew === "older" ? "nodes.agentOutdated" : "nodes.agentAhead")}
          </Badge>
        )}

        {/* Six actions, as icons. Spelled out they crowded the row and pushed the
            server's own name off a narrow screen; the words live on as the
            accessible name and the hover title. */}
        <span className="ml-auto flex shrink-0 items-center gap-0.5">
          <IconButton title={t("nav.settings")} onClick={() => setEditingRouting(true)}>
            <IconGear size={16} />
          </IconButton>
          <IconButton title={t("nodes.diagnostics")} onClick={() => setShowingHealth(true)}>
            <IconPulse size={16} />
          </IconButton>
          <IconButton title={t("xray.configTitle")} onClick={() => setShowingConfig(true)}>
            <IconBraces size={16} />
          </IconButton>
          <IconButton title={t("manage.logs")} onClick={() => setShowingLogs(true)}>
            <IconTerminal size={16} />
          </IconButton>
          <IconButton
            title={
              node.xray_restart === "pending"
                ? t("nodes.restartQueuedHint")
                : t("nodes.restartXray")
            }
            disabled={
              restarting ||
              node.xray_restart === "pending" ||
              (!node.is_local && (!node.enabled || !node.joined))
            }
            onClick={doXrayRestart}
          >
            <IconRestart
              size={16}
              className={node.xray_restart === "pending" ? "animate-spin" : undefined}
            />
          </IconButton>
          {!node.is_local && (
            <Dropdown
              align="end"
              width={210}
              trigger={
                <span
                  title={t("nodes.manageNode")}
                  className="inline-flex size-8 items-center justify-center rounded-lg text-gray-600 transition hover:bg-gray-100 active:scale-90"
                >
                  <IconDots size={16} />
                </span>
              }
            >
              {/* The access switch lives here rather than in the header: a toggle
                  among five icon buttons is a mis-click waiting to happen, and this
                  one takes a server out of every subscription. */}
              <DropdownItem onClick={() => toggleEnabled(!node.enabled)}>
                {t(node.enabled ? "usersPanel.disable" : "usersPanel.enable")}
              </DropdownItem>
              <DropdownItem onClick={doUpdate}>
                {t("nodes.update")}{node.version_skew ? ` ${t("nodes.newVersionSuffix")}` : ""}
              </DropdownItem>
              {/* One reinstall action: the dialog offers the command and the SSH way.
                  They were two menu items (one just issued the command for
                  the same reinstall), which read as two different operations. */}
              <DropdownItem onClick={() => setReconnecting(true)}>
                {t("nodes.reinstall")}
              </DropdownItem>
              <DropdownDivider />
              <DropdownItem color="red" onClick={() => setRemoveOpen(true)}>
                {t("common.delete")}
              </DropdownItem>
            </Dropdown>
          )}
        </span>
      </header>

      {/* On a phone this is three stacked lines — who and what it carried, how it is,
          then the load bars full width. On a wider screen the same three parts sit in
          one row; `lg:contents` dissolves the mobile pairing so ordering can put the
          traffic back on the right. */}
      <div className="p-3.5 lg:flex lg:flex-wrap lg:items-center lg:gap-x-5">
        <div className="flex items-start justify-between gap-3 lg:contents">
          <span className="flex min-w-0 flex-col gap-0.5 lg:order-1 lg:min-w-45">
            <Mono className="truncate text-xs text-gray-800">{node.host || "—"}</Mono>
            <span className="truncate text-xs text-ink-muted">
              <span
                title={
                  !node.is_local && node.sync_fails > 0
                    ? t("nodes.syncFailsHint", { count: node.sync_fails })
                    : undefined
                }
                className={cn(
                  state.tone === "success" && "text-success",
                  state.tone === "warning" && "text-warning",
                  state.tone === "danger" && "text-danger",
                )}
              >
                {state.label}
              </span>
              {sublineTail && ` · ${sublineTail}`}
            </span>
          </span>

          <span className="flex shrink-0 flex-col items-end lg:order-3 lg:ml-auto">
            <Mono className="text-sm text-ink">{fmtBytes(traffic)}</Mono>
            <span className="text-[11px] text-ink-muted">{t("nodes.trafficToday")}</span>
          </span>
        </div>

        {/* A server that has never reported says so, rather than showing three empty
            bars — which read as an idle machine. */}
        {node.has_host_stats ? (
          <div className="mt-2.5 flex min-w-0 flex-col gap-1.5 lg:order-2 lg:mt-0 lg:flex-row lg:items-center lg:gap-5">
            <MiniBar label="CPU" percent={node.cpu_percent} className="w-full lg:w-37" />
            <MiniBar
              label="RAM"
              percent={pct(node.mem_used, node.mem_total)}
              className="w-full lg:w-37"
            />
            <MiniBar
              label={t("overview.disk")}
              percent={pct(node.disk_used, node.disk_total)}
              className="w-full lg:w-37"
            />
          </div>
        ) : (
          <span className="mt-2 block text-xs text-ink-muted lg:order-2 lg:mt-0">
            {t("overview.noStats")}
          </span>
        )}
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
        title={t("nodes.diagnosticsOf", { name: serverName(node) })}
        size="lg"
      >
        <HealthPanel nodeId={node.id} />
      </Modal>
      {showingConfig && (
        <XrayConfigView
          nodeId={node.id}
          title={t("nodes.xrayConfigOf", { name: node.name })}
          note={
            node.is_local
              ? undefined
              : t("nodes.configNote")
          }
          onClose={() => setShowingConfig(false)}
        />
      )}
    </section>
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

// A factory, not a constant: a constant would freeze the labels in whichever
// language happened to be active when this module was first imported.
const nodeLogFilters = () => [
  { value: "all", label: i18n.t("logs.all") },
  { value: "access", label: i18n.t("logs.access") },
  { value: "info", label: i18n.t("logs.info") },
  { value: "warning", label: i18n.t("logs.warning") },
  { value: "error", label: i18n.t("logs.error") },
];

// NodeLogsDialog streams a node's recent logs. It polls the panel, which asks the
// node to include its log tail on its next sync (agent + Xray), so the view stays
// fresh while open (with up to one sync interval of latency). Tabs filter by level.
function NodeLogsDialog({ node, onClose }: { node: NodeView; onClose: () => void }) {
  const { t } = useTranslation();
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
    // The same frame the panel's own log viewer uses: a fixed-height window. A modal
    // that sizes to its content shrank to a couple of lines while a node was still
    // sending its first ones, and grew under the reader as they arrived.
    <ToolDialog
      title={t("nodes.logsOf", { name: node.name })}
      onClose={onClose}
      headerExtra={
        <SegmentedControl data={nodeLogFilters()} value={level} onChange={setLevel} />
      }
    >
      <div className="flex-1 overflow-auto bg-gray-50 p-3 font-mono text-xs leading-relaxed">
        {!loaded ? (
          <p className="text-gray-400">{t("nodes.requestingLogs")}</p>
        ) : lines.length === 0 ? (
          <p className="text-gray-400">{t("nodes.logsPending")}</p>
        ) : shown.length === 0 ? (
          <p className="text-gray-400">{t("logs.noLinesAtLevel")}</p>
        ) : (
          shown.map((l, i) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: a log stream is positional — lines repeat verbatim and only ever append
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
    </ToolDialog>
  );
}

export function NodesPanel() {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [decoys, setDecoys] = useState<string[]>([]);
  // Geo categories feed the routing editor's domain/IP suggestions (same list for
  // the master and every node — one panel-side geosite/geoip).
  const [geo, setGeo] = useState<GeoCategories>({ geosite: [], geoip: [], iplist: [] });
  const [adding, setAdding] = useState(false);
  const [installCmd, setInstallCmd] = useState<string | null>(null);
  const [panelVersion, setPanelVersion] = useState("");

  const load = () =>
    listNodes()
      .then((r) => setNodes(r.nodes))
      .catch((e) => notifyError(errMessage(e)));

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load();
    getSettings()
      .then((s) => setDecoys(s.decoy_templates || []))
      .catch(() => {});
    getMe()
      .then((m) => setPanelVersion(m.version || ""))
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
  // restarted badge on screen until the next lazy tick, well past the few
  // seconds the server means it to be shown. Otherwise this is just the liveness
  // refresh keeping online/offline badges current, and it stays lazy.
  const showingRestart = !!nodes?.some((n) => n.xray_restart);
  // biome-ignore lint/correctness/useExhaustiveDependencies: the cadence is the only thing this re-reads; load is redefined every render and would restart the timer on each one
  useEffect(() => {
    // 8s while the page is open. A node's own report lands every 30–60s (the panel
    // holds its long-poll that long), so this cannot make node data fresher than the
    // node makes it — but everything the PANEL knows already, an enable, a queued
    // restart, an agent version after an update, shows up within a tick instead of
    // sitting on screen stale for a quarter of a minute.
    const t = setInterval(load, showingRestart ? 2000 : 8000);
    return () => clearInterval(t);
  }, [showingRestart]);

  if (nodes === null) return <CenterLoader />;

  const remoteCount = nodes.filter((n) => !n.is_local).length;
  // Two different problems, two different fixes: nodes behind the panel are what
  // "Update all" is for, while a node ahead of the panel means the panel is the
  // one to update — telling the operator to pull those "up" would be backwards.
  const anyBehind = nodes.some((n) => n.online && agentSkew(n, panelVersion) === "older");
  const anyAhead = nodes.some((n) => n.online && agentSkew(n, panelVersion) === "newer");

  const updateAll = async () => {
    try {
      const r = await updateAllNodes();
      notifySuccess(t("nodes.updateStarted", { count: r.nodes }));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => setAdding(true)}>
          {t("nodes.addNode")}
        </Button>
        {remoteCount > 0 && (
          <Button size="sm" variant="outline" color="gray" onClick={updateAll}>
            {t("nodes.updateAll")}{anyBehind ? " ⚠" : ""}
          </Button>
        )}
      </div>

      {anyBehind && (
        <p className="accent-tint rounded-lg px-3 py-2 text-xs text-accent">
          {t("nodes.someAgentsOutdated")}
        </p>
      )}

      {anyAhead && (
        <p className="accent-tint rounded-lg px-3 py-2 text-xs text-accent">
          {t("nodes.someAgentsAhead")}
        </p>
      )}

      {/* A vertical list of sections, one per server — not a grid of cards. A fleet is
          read top to bottom, and a row that wraps to a second column is a row an
          operator scrolls past. */}
      {nodes.map((n) => (
        <NodeCard
          key={n.id}
          node={n}
          decoys={decoys}
          geo={geo}
          panelVersion={panelVersion}
          onChanged={load}
          onRegen={setInstallCmd}
        />
      ))}

      {/* One server is not a fleet: say what the page is for instead of leaving the
          master alone under a heading about servers. */}
      {remoteCount === 0 && (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-brand-600/10 bg-white px-4 py-10 text-center">
          <p className="text-sm font-semibold text-ink">{t("nodes.onlyThisServer")}</p>
          <p className="max-w-md text-xs text-ink-muted">{t("nodes.onlyThisServerHint")}</p>
          <Button size="sm" className="mt-2" onClick={() => setAdding(true)}>
            {t("nodes.addNode")}
          </Button>
        </div>
      )}

      <ExternalServers />

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
