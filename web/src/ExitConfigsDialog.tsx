import { useState } from "react";
import { useTranslation } from "react-i18next";
import { type ExitConfig, type NodeView, setExitConfigs } from "./api";
import { relayLaneLabel } from "./ExternalDelivery";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { serverName } from "./NodeStatus";
import { Button, cn, Modal, Switch, TextInput } from "./ui";

// A server's WARP and Opera handed to users as connections of their own: a VLESS
// entry on the lanes picked whose traffic all leaves through that way out. Who gets
// them is up to the access groups, where each shows up beside the server's lanes.

const KINDS = ["warp", "opera"] as const;
const LANES = ["vless", "reality"] as const;

function laneOn(n: NodeView, lane: string): boolean {
  return lane === "reality" ? n.reality_enabled && !!n.reality_public_key : n.vless_enabled;
}

export function ExitConfigsDialog({ node, onClose, onSaved }: { node: NodeView; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { busy, run } = useAction();
  const [exits, setExits] = useState<ExitConfig[]>(() =>
    KINDS.map((kind) => node.exit_configs?.find((c) => c.kind === kind) ?? { kind, lanes: [], name: "" }),
  );
  const patch = (kind: string, d: Partial<ExitConfig>) => setExits(exits.map((c) => (c.kind === kind ? { ...c, ...d } : c)));

  const runs = (kind: string) => (kind === "warp" ? node.warp_enabled && node.warp_registered : node.opera_enabled);
  const label = (kind: string) => (kind === "warp" ? "WARP" : "Opera");

  const save = () =>
    run(async () => {
      await setExitConfigs(
        node.id,
        exits.filter((c) => c.lanes.length > 0 || (c.name ?? "").trim() !== ""),
      );
      notifySuccess(t("exits.saved"));
      onSaved();
    }).catch((e) => notifyError(errMessage(e)));

  return (
    <Modal
      open
      onClose={onClose}
      title={t("exits.title")}
      subtitle={serverName(node)}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} loading={busy}>
            {t("common.save")}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-xs leading-relaxed text-ink-muted">{t("exits.hint")}</p>
        {exits.map((c) => {
          const on = c.lanes.length > 0;
          const ok = runs(c.kind);
          return (
            <section
              key={c.kind}
              className={cn("rounded-xl border p-3.5 transition", on ? "border-brand-600/40 bg-brand-50/50" : "border-gray-200")}
            >
              <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-semibold text-ink">{label(c.kind)}</p>
                  <p className={cn("text-xs", ok ? "text-ink-muted" : "text-warning")}>
                    {ok ? t(c.kind === "warp" ? "exits.warpHint" : "exits.operaHint") : t("exits.off", { name: label(c.kind) })}
                  </p>
                </div>
                <Switch
                  checked={on}
                  onChange={(v) =>
                    patch(c.kind, { lanes: v ? LANES.filter((l) => laneOn(node, l)).slice(0, 1) : [] })
                  }
                />
              </div>
              {on && (
                <div className="mt-3 flex flex-col gap-3 border-t border-gray-100 pt-3">
                  <div>
                    <p className="mb-1.5 text-xs font-medium text-ink">{t("exits.lanes")}</p>
                    <div className="flex flex-wrap gap-1.5">
                      {LANES.map((lane) => {
                        const runsLane = laneOn(node, lane);
                        const picked = c.lanes.includes(lane);
                        return (
                          <button
                            key={lane}
                            type="button"
                            disabled={!runsLane && !picked}
                            title={runsLane ? undefined : t("extRoute.laneOff")}
                            onClick={() =>
                              patch(c.kind, {
                                lanes: picked ? c.lanes.filter((l) => l !== lane) : [...c.lanes, lane],
                              })
                            }
                            className={cn(
                              "rounded-full border px-2.5 py-0.5 text-xs transition",
                              picked
                                ? "border-brand-600 bg-brand-600 text-white"
                                : runsLane
                                  ? "border-gray-300 text-ink hover:border-brand-600/50"
                                  : "cursor-not-allowed border-dashed border-gray-200 text-ink-muted line-through",
                            )}
                          >
                            {relayLaneLabel(lane)}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                  <TextInput
                    label={t("exits.name")}
                    value={c.name ?? ""}
                    onChange={(v) => patch(c.kind, { name: v })}
                    placeholder={node.is_local ? label(c.kind) : `${serverName(node)} · ${label(c.kind)}`}
                  />
                </div>
              )}
            </section>
          );
        })}
        <p className="text-xs leading-relaxed text-ink-muted">{t("exits.groupsNote")}</p>
      </div>
    </Modal>
  );
}
