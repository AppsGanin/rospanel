import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  getPluginWidgets,
  getUserPluginFields,
  listPluginActions,
  type PluginAction,
  type PluginFieldsBlock,
  type PluginWidget,
  runPluginAction,
} from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, Mono, Panel, useConfirm } from "./ui";

// What plugins add to the panel's own screens: buttons, values on a user's card,
// dashboard tiles. Everything a plugin returns is shown as text — a plugin never
// gets to put markup in front of an admin.

// The actions change only when plugins do; one request per page load is plenty.
let actionsOnce: Promise<PluginAction[]> | null = null;
function useActions(): PluginAction[] {
  const [actions, setActions] = useState<PluginAction[]>([]);
  useEffect(() => {
    actionsOnce ??= listPluginActions()
      .then((r) => r.actions)
      .catch(() => []);
    let live = true;
    actionsOnce.then((a) => live && setActions(a));
    return () => {
      live = false;
    };
  }, []);
  return actions;
}

// forgetPluginActions drops the cached list (after a plugin was switched on or off).
export function forgetPluginActions() {
  actionsOnce = null;
}

// PluginActionButtons draws the buttons plugins add for a scope: on a user's card
// ("user"), the users list's selection ("users"), or a plugin's own card ("global",
// with plugin set).
export function PluginActionButtons({
  scope,
  userIds = [],
  plugin,
  onDone,
}: {
  scope: PluginAction["scope"];
  userIds?: number[];
  plugin?: string;
  onDone?: () => void;
}) {
  const { t } = useTranslation();
  const actions = useActions().filter((a) => a.scope === scope && (!plugin || a.plugin === plugin));
  const [busy, setBusy] = useState("");
  const { confirm, confirmNode } = useConfirm();
  if (actions.length === 0) return null;

  const press = async (a: PluginAction) => {
    if (a.confirm && !(await confirm({ title: a.label, body: t("plugins.actionConfirm", { count: userIds.length }) }))) {
      return;
    }
    const id = `${a.plugin}/${a.key}`;
    setBusy(id);
    try {
      const r = await runPluginAction(a.plugin, a.key, userIds);
      const msg = r.message || t("plugins.actionDone");
      if (r.ok) notifySuccess(msg);
      else notifyError(msg);
      onDone?.();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy("");
    }
  };

  return (
    <>
      {actions.map((a) => (
        <Button
          key={`${a.plugin}/${a.key}`}
          size="xs"
          variant="outline"
          color="gray"
          loading={busy === `${a.plugin}/${a.key}`}
          disabled={busy !== ""}
          onClick={() => press(a)}
        >
          {a.label}
        </Button>
      ))}
      {confirmNode}
    </>
  );
}

// UserPluginPanel is what plugins show on a user's card: their values and buttons.
export function UserPluginPanel({ userId }: { userId: number }) {
  const { t } = useTranslation();
  const [blocks, setBlocks] = useState<PluginFieldsBlock[]>([]);
  const hasButtons = useActions().some((a) => a.scope === "user");
  useEffect(() => {
    getUserPluginFields(userId)
      .then((r) => setBlocks(r.plugins))
      .catch(() => setBlocks([]));
  }, [userId]);
  if (blocks.length === 0 && !hasButtons) return null;
  return (
    <Panel title={t("plugins.title")} pad bodyClassName="flex flex-col gap-3">
      {blocks.map((b) => (
        <div key={b.plugin}>
          <p className="mb-1 text-[11px] font-semibold text-ink-muted">{b.name}</p>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            {b.fields.map((f) => (
              <div key={f.key} className="contents">
                <dt className="text-ink-muted">{f.label}</dt>
                <dd className="break-words text-ink">{f.value}</dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
      {hasButtons && (
        <div className="flex flex-wrap gap-1.5">
          <PluginActionButtons scope="user" userIds={[userId]} />
        </div>
      )}
    </Panel>
  );
}

// PluginWidgets are the plugins' dashboard tiles; nothing at all without any.
export function PluginWidgets() {
  const { t } = useTranslation();
  const [widgets, setWidgets] = useState<PluginWidget[]>([]);
  useEffect(() => {
    const load = () =>
      getPluginWidgets()
        .then((r) => setWidgets(r.widgets))
        .catch(() => {});
    load();
    const id = setInterval(load, 60_000);
    return () => clearInterval(id);
  }, []);
  if (widgets.length === 0) return null;
  return (
    <div className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-3">
      {widgets.map((w) => (
        <Panel key={`${w.plugin}/${w.key}`} title={w.label} pad>
          {w.error || !w.data ? (
            <p className="text-xs text-ink-muted">{t("plugins.widgetNoData")}</p>
          ) : (
            <WidgetBody data={w.data} />
          )}
        </Panel>
      ))}
    </div>
  );
}

function WidgetBody({ data }: { data: NonNullable<PluginWidget["data"]> }) {
  switch (data.type) {
    case "stat":
      return (
        <div>
          <Mono className="text-2xl font-semibold text-ink">{String(data.value)}</Mono>
          {data.hint && <p className="mt-1 text-xs text-ink-muted">{data.hint}</p>}
        </div>
      );
    case "list":
      return (
        <ul className="flex list-disc flex-col gap-0.5 pl-4 text-xs text-ink">
          {(data.items ?? []).slice(0, 50).map((it, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: a plugin's list has no ids
            <li key={i}>{String(it)}</li>
          ))}
        </ul>
      );
    case "table":
      return (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr>
                {(data.columns ?? []).map((c, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: columns have no ids
                  <th key={i} className="pb-1 text-left font-semibold text-ink-muted">
                    {String(c)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data.rows ?? []).slice(0, 50).map((r, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: rows have no ids
                <tr key={i} className="border-t border-gray-100">
                  {(Array.isArray(r) ? r : []).map((c, j) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: cells have no ids
                    <td key={j} className="py-1 pr-2 text-ink">
                      {String(c)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
  }
  return null;
}
