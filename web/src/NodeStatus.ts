import i18n from "./i18n";
import { type NodeView } from "./api";
import { type Tone } from "./ui";

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

export function agentSkew(node: NodeView, panelVersion: string): AgentSkew {
  if (node.is_local || !node.node_version || !panelVersion) return "";
  const d = cmpVersion(node.node_version, panelVersion);
  return d < 0 ? "older" : d > 0 ? "newer" : "";
}
