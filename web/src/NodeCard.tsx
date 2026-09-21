import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  deleteNode,
  getNodeLogs,
  setNodeEnabled,
  restartNodeXray,
  restartXray,
  updateNodeVersion,
  type GeoCategories,
  type NodeView,
} from "./api";
import { fmtBytes, fmtStamp } from "./format";
import { HealthPanel } from "./HealthPanel";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { EMPTY_STEP_UP, type StepUp, StepUpFields, stepUpReady, useTotpEnabled } from "./stepup";
import { XrayConfigView } from "./XrayConfig";
import { XrayLogs } from "./XrayLogs";
import {
  Badge,
  Button,
  cn,
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
  MiniBar,
  Modal,
  Mono,
  SegmentedControl,
  ToolDialog,
  useConfirm,
} from "./ui";
import { ReconnectDialog } from "./AddNodeDialog";
import { MasterSettingsDialog } from "./MasterSettingsDialog";
import { NodeSettingsDialog } from "./NodeSettingsDialog";
import { agentSkew, nodeState, serverName } from "./NodeStatus";

function fmtSeen(unix: number): string {
  if (!unix) return i18n.t("nodes.neverJoined");
  const ago = Math.floor(Date.now() / 1000) - unix;
  if (ago < 60) return i18n.t("lastSeen.justNow");
  if (ago < 3600) return i18n.t("lastSeen.minutes", { n: Math.floor(ago / 60) });
  if (ago < 86400) return i18n.t("lastSeen.hours", { n: Math.floor(ago / 3600) });
  return fmtStamp(unix);
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

export function NodeCard({
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
