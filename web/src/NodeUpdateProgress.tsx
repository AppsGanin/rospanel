import { useTranslation } from "react-i18next";
import type { NodeView } from "./api";
import { cmpVersion, serverName } from "./NodeStatus";
import { IconCheck, Modal, Mono, Spinner } from "./ui";

// NodeUpdateRun is an update the operator just asked of some nodes: which ones, the
// release they are going to (when the panel could read it) and what each ran before.
export type NodeUpdateRun = {
  ids: number[];
  target: string;
  from: Record<number, string>;
  started: number;
};

// A node that has said nothing for this long is shown as slow, not as still updating:
// the download and restart take well under a minute, and a node keeps retrying a
// release without its binaries for a quarter of an hour.
const SLOW_MS = 3 * 60 * 1000;

type UpdateState = "done" | "failed" | "offline" | "slow" | "pending";

const ORDER: Record<UpdateState, number> = { failed: 0, pending: 1, slow: 2, offline: 3, done: 4 };

function updateState(n: NodeView, run: NodeUpdateRun, now: number): UpdateState {
  const done = run.target
    ? !!n.node_version && cmpVersion(n.node_version, run.target) >= 0
    : !!n.node_version && n.node_version !== run.from[n.id];
  if (done) return "done";
  if (n.update_error) return "failed";
  if (!n.online) return "offline";
  if (now - run.started > SLOW_MS) return "slow";
  return "pending";
}

// runSettled says the dialog has nothing left to wait for.
export function runSettled(run: NodeUpdateRun, nodes: NodeView[]): boolean {
  const now = Date.now();
  return nodes
    .filter((n) => run.ids.includes(n.id))
    .every((n) => updateState(n, run, now) !== "pending");
}

// NodeUpdateProgress follows a node update the way the panel's own update is
// followed: a dialog that says where each node is, fed by the list's polling.
export function NodeUpdateProgress({
  run,
  nodes,
  onClose,
}: {
  run: NodeUpdateRun | null;
  nodes: NodeView[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  if (!run) return null;
  const now = Date.now();
  // What needs a look first: failures, then what is still moving, then what waits on
  // the node; the finished ones sink to the bottom of a long fleet.
  const rows = nodes
    .filter((n) => run.ids.includes(n.id))
    .map((n) => ({ n, st: updateState(n, run, now) }))
    .sort((a, b) => ORDER[a.st] - ORDER[b.st]);
  const done = rows.filter((r) => r.st === "done").length;
  const failed = rows.filter((r) => r.st === "failed").length;
  return (
    <Modal
      open
      onClose={onClose}
      title={run.target ? t("nodes.upd.titleTo", { version: run.target }) : t("nodes.upd.title")}
      // The tally stays put while a long fleet scrolls under it.
      toolbar={
        <div className="py-3">
          <div className="flex items-baseline justify-between gap-3 text-xs">
            <span className="text-ink">{t("nodes.upd.summary", { done, total: rows.length })}</span>
            {failed > 0 && <span className="text-danger">{t("nodes.upd.failedCount", { count: failed })}</span>}
          </div>
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-gray-100">
            <div
              className="h-full rounded-full bg-brand-600 transition-all"
              style={{ width: `${rows.length ? (done / rows.length) * 100 : 0}%` }}
            />
          </div>
        </div>
      }
    >
      <div className="divide-y divide-gray-100">
        {rows.map(({ n, st }) => (
          <div key={n.id} className="py-1.5 first:pt-0 last:pb-0">
            <div className="flex items-center gap-2.5">
              <span className="flex size-4 shrink-0 items-center justify-center">
                {st === "pending" && <Spinner size={14} />}
                {st === "done" && <IconCheck size={14} className="text-success" />}
                {(st === "failed" || st === "slow" || st === "offline") && (
                  <span className={st === "failed" ? "size-2 rounded-full bg-red-500" : "size-2 rounded-full bg-gray-400"} />
                )}
              </span>
              <span className="min-w-0 flex-1 truncate text-sm text-ink">{serverName(n)}</span>
              <span className={st === "failed" ? "shrink-0 text-xs text-danger" : "shrink-0 text-xs text-ink-muted"}>
                {st === "done" ? <Mono>{`v${(n.node_version || "").replace(/^v/, "")}`}</Mono> : t(`nodes.upd.${st}`)}
              </span>
            </div>
            {st === "failed" && n.update_error && (
              <Mono className="mt-0.5 block break-all ps-6.5 text-[11px] text-ink-muted">{n.update_error}</Mono>
            )}
          </div>
        ))}
      </div>
    </Modal>
  );
}
