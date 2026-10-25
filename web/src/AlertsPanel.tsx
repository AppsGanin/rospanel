// Alert rules: a server value (CPU, memory, disk, speed, users online) that stays past
// a threshold for a set time tells the admins; this is the list, the editor and the
// history of what fired.
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type AlertEvent,
  type AlertMetric,
  type AlertRule,
  type AlertRuleInput,
  createAlert,
  deleteAlert,
  getAlertEvents,
  getAlerts,
  type NodeView,
  updateAlert,
} from "./api";
import { fmtDuration, fmtStamp } from "./format";
import i18n, { td } from "./i18n";
import { serverName } from "./NodeStatus";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan } from "./role";
import { DialogTabs } from "./ServerDialogParts";
import {
  Badge,
  Button,
  CenterLoader,
  EmptyState,
  IconButton,
  IconPencil,
  IconPlus,
  IconTrash,
  Modal,
  ReadOnly,
  Select,
  SettingRow,
  Switch,
  TextInput,
  useConfirm,
} from "./ui";

const EVERY_SERVER = -1;
const PAGE = 50;
// Minutes the duration picker offers; a rule set over the API to anything else keeps
// its own value in the list.
const FOR_MINUTES = [0, 1, 2, 5, 10, 15, 30, 60];

// The rules "Add suggested rules" creates: all for every server.
const SUGGESTED: { name: "alerts.suggestCpu" | "alerts.suggestMem" | "alerts.suggestDisk"; metric: string; threshold: number; for_secs: number }[] = [
  { name: "alerts.suggestCpu", metric: "cpu_percent", threshold: 90, for_secs: 600 },
  { name: "alerts.suggestMem", metric: "mem_percent", threshold: 90, for_secs: 600 },
  { name: "alerts.suggestDisk", metric: "disk_percent", threshold: 90, for_secs: 0 },
];

const metricLabel = (key: string) => td(`alerts.metric_${key}`);

// fmtVal is a reading with its unit: "90%", "12 Mbit/s", "40".
function fmtVal(v: number, unit: string): string {
  const n = Number(v.toFixed(1));
  if (unit === "%") return `${n}%`;
  if (unit === "users") return String(n);
  return `${n} ${i18n.t("alerts.unitMbps")}`;
}

// fmtFor is a span held, in whole hours or minutes where it comes out even.
function fmtFor(secs: number): string {
  if (secs > 0 && secs % 3600 === 0) return i18n.t("dur.h", { n: secs / 3600 });
  if (secs > 0 && secs % 60 === 0) return i18n.t("dur.m", { n: secs / 60 });
  return fmtDuration(secs);
}

function sentence(r: AlertRuleInput, unit: string): string {
  const args = {
    metric: metricLabel(r.metric),
    op: i18n.t(r.op === "gt" ? "alerts.opGt" : "alerts.opLt"),
    value: fmtVal(r.threshold, unit),
    dur: fmtFor(r.for_secs),
  };
  return i18n.t(r.for_secs > 0 ? "alerts.sentenceFor" : "alerts.sentence", args);
}

function RuleRow({
  rule,
  unit,
  scope,
  canManage,
  busy,
  onToggle,
  onEdit,
  onDelete,
}: {
  rule: AlertRule;
  unit: string;
  scope: string;
  canManage: boolean;
  busy: boolean;
  onToggle: (on: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 border-t border-gray-100 px-3.5 py-2 first:border-t-0">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-semibold text-ink">{rule.name}</span>
          {rule.firing > 0 && (
            <Badge color="red" size="xs">
              {t("alerts.firing")}
            </Badge>
          )}
        </div>
        <p className="truncate text-xs text-ink-muted">
          {sentence(rule, unit)} · {scope}
        </p>
      </div>
      <Switch checked={rule.enabled} onChange={onToggle} disabled={busy} label={rule.name} />
      {canManage && (
        <>
          <IconButton title={t("common.edit")} onClick={onEdit}>
            <IconPencil />
          </IconButton>
          <IconButton color="red" title={t("common.delete")} onClick={onDelete}>
            <IconTrash />
          </IconButton>
        </>
      )}
    </div>
  );
}

function RuleEditor({
  rule,
  nodes,
  metrics,
  onClose,
  onSaved,
}: {
  rule: AlertRule | null;
  nodes: NodeView[];
  metrics: AlertMetric[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(rule?.name ?? "");
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [metric, setMetric] = useState(rule?.metric ?? metrics[0]?.key ?? "cpu_percent");
  const [op, setOp] = useState<"gt" | "lt">(rule?.op ?? "gt");
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? 90));
  const [forSecs, setForSecs] = useState(rule?.for_secs ?? 600);
  const [nodeID, setNodeID] = useState(rule?.node_id ?? EVERY_SERVER);
  const [saving, setSaving] = useState(false);
  const unit = metrics.find((m) => m.key === metric)?.unit ?? "%";

  const forList = [...FOR_MINUTES.map((m) => m * 60)];
  if (!forList.includes(forSecs)) forList.push(forSecs);
  forList.sort((a, b) => a - b);

  const save = async () => {
    const th = Number(threshold.trim().replace(",", "."));
    if (!name.trim()) return notifyError(t("alerts.nameRequired"));
    if (threshold.trim() === "" || !Number.isFinite(th)) return notifyError(t("alerts.thresholdInvalid"));
    const body: AlertRuleInput = {
      name: name.trim(),
      enabled,
      metric,
      op,
      threshold: th,
      for_secs: forSecs,
      node_id: nodeID,
    };
    setSaving(true);
    try {
      if (rule) await updateAlert(rule.id, body);
      else await createAlert(body);
      notifySuccess(t("common.saved"));
      onSaved();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t(rule ? "alerts.editTitle" : "alerts.editNew")}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="outline" color="gray" size="sm" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button size="sm" onClick={save} loading={saving}>
            {t("common.save")}
          </Button>
        </div>
      }
    >
      <div className="flex flex-col">
        <SettingRow inset>
          <TextInput label={t("alerts.name")} value={name} onChange={setName} autoFocus />
        </SettingRow>
        <SettingRow
          inset
          label={t("alerts.server")}
          field={
            <Select
              value={String(nodeID)}
              onChange={(v) => setNodeID(Number(v))}
              data={[
                { value: String(EVERY_SERVER), label: t("alerts.scopeAll") },
                ...nodes.map((n) => ({ value: String(n.id), label: serverName(n) })),
              ]}
            />
          }
        />
        <SettingRow
          inset
          label={t("alerts.metric")}
          field={
            <Select
              value={metric}
              onChange={setMetric}
              data={metrics.map((m) => ({ value: m.key, label: metricLabel(m.key) }))}
            />
          }
        />
        <SettingRow
          inset
          label={t("alerts.condition")}
          field={
            <div className="flex items-center gap-2">
              <Select
                value={op}
                onChange={(v) => setOp(v === "lt" ? "lt" : "gt")}
                data={[
                  { value: "gt", label: t("alerts.opGt") },
                  { value: "lt", label: t("alerts.opLt") },
                ]}
              />
              <TextInput
                value={threshold}
                onChange={setThreshold}
                inputMode="text"
                className="w-20"
                ariaLabel={t("alerts.threshold")}
              />
              <span className="text-xs text-ink-muted" dir="auto">
                {unit === "%" ? "%" : unit === "users" ? "" : t("alerts.unitMbps")}
              </span>
            </div>
          }
        />
        <SettingRow
          inset
          label={t("alerts.lasts")}
          hint={t("alerts.lastsHint")}
          field={
            <Select
              value={String(forSecs)}
              onChange={(v) => setForSecs(Number(v))}
              data={forList.map((s) => ({
                value: String(s),
                label: s === 0 ? t("alerts.forNow") : fmtFor(s),
              }))}
            />
          }
        />
        <SettingRow
          inset
          label={t("alerts.enabled")}
          field={<Switch checked={enabled} onChange={setEnabled} label={t("alerts.enabled")} />}
        />
      </div>
    </Modal>
  );
}

function History({ metrics }: { metrics: AlertMetric[] }) {
  const { t } = useTranslation();
  const [events, setEvents] = useState<AlertEvent[] | null>(null);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async (before: number) => {
    setLoading(true);
    try {
      const r = await getAlertEvents(before, PAGE);
      setEvents((cur) => (before ? [...(cur ?? []), ...r.events] : r.events));
      setMore(r.events.length >= PAGE);
    } catch (e) {
      notifyError(errMessage(e));
      setEvents((cur) => cur ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load(0);
  }, [load]);

  if (!events) return <CenterLoader />;
  if (events.length === 0) return <EmptyState title={t("alerts.historyEmpty")} />;
  return (
    <div className="flex flex-col">
      {events.map((e) => {
        const unit = metrics.find((m) => m.key === e.metric)?.unit ?? "%";
        const reading = fmtVal(e.value, unit);
        const worst = e.peak !== e.value ? ` · ${t("alerts.peak", { v: fmtVal(e.peak, unit) })}` : "";
        return (
          <div
            key={e.id}
            className="flex items-start justify-between gap-3 border-t border-gray-100 px-3.5 py-2 first:border-t-0"
          >
            <div className="min-w-0">
              <p className="truncate text-sm font-semibold text-ink">{e.rule_name}</p>
              <p className="truncate text-xs text-ink-muted">
                {e.node_name} · {reading}
                {worst} ({sentence({ name: e.rule_name, enabled: true, metric: e.metric, op: e.op, threshold: e.threshold, for_secs: 0, node_id: e.node_id }, unit)})
              </p>
            </div>
            <div className="flex shrink-0 flex-col items-end gap-0.5 text-xs text-ink-muted">
              <span>{fmtStamp(e.fired_at)}</span>
              {e.resolved_at === 0 ? (
                <Badge color="red" size="xs">
                  {t("alerts.firing")}
                </Badge>
              ) : (
                <span>{fmtDuration(Math.max(1, e.resolved_at - e.fired_at))}</span>
              )}
            </div>
          </div>
        );
      })}
      {more && (
        <div className="pt-2">
          <Button variant="light" color="gray" size="sm" fullWidth nav loading={loading} onClick={() => load(events[events.length - 1].id)}>
            {t("common.showMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

// AlertsDialog is the rules and their history. Editing is servers.manage's; reading
// the list and the history is servers.view's.
export function AlertsDialog({ nodes, onClose }: { nodes: NodeView[]; onClose: () => void }) {
  const { t } = useTranslation();
  const canManage = useCan("servers.manage");
  const [tab, setTab] = useState<"rules" | "history">("rules");
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [metrics, setMetrics] = useState<AlertMetric[]>([]);
  const [editing, setEditing] = useState<AlertRule | "new" | null>(null);
  const [busy, setBusy] = useState(false);
  const { confirm, confirmNode } = useConfirm();

  const load = useCallback(
    () =>
      getAlerts()
        .then((r) => {
          setRules(r.rules);
          setMetrics(r.metrics);
        })
        .catch((e) => notifyError(errMessage(e))),
    [],
  );

  useEffect(() => {
    load();
  }, [load]);

  const unitOf = (key: string) => metrics.find((m) => m.key === key)?.unit ?? "%";
  const scopeOf = (id: number) => {
    if (id === EVERY_SERVER) return t("alerts.scopeAll");
    const n = nodes.find((x) => x.id === id);
    return n ? serverName(n) : String(id);
  };

  const toggle = async (r: AlertRule, enabled: boolean) => {
    setBusy(true);
    try {
      await updateAlert(r.id, { ...r, enabled });
      await load();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (r: AlertRule) => {
    const ok = await confirm({
      title: t("alerts.deleteTitle"),
      body: t("alerts.deleteBody", { name: r.name }),
      danger: true,
      confirmLabel: t("common.delete"),
    });
    if (!ok) return;
    try {
      await deleteAlert(r.id);
      notifySuccess(t("alerts.deleted"));
      await load();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const addSuggested = async () => {
    setBusy(true);
    try {
      for (const s of SUGGESTED) {
        await createAlert({
          name: t(s.name),
          enabled: true,
          metric: s.metric,
          op: "gt",
          threshold: s.threshold,
          for_secs: s.for_secs,
          node_id: EVERY_SERVER,
        });
      }
      notifySuccess(t("alerts.suggestedAdded"));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
      await load();
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={t("alerts.title")}
      subtitle={t("alerts.about")}
      toolbar={
        <DialogTabs
          value={tab}
          onChange={(v) => setTab(v === "history" ? "history" : "rules")}
          tabs={[
            { value: "rules", label: t("alerts.tabRules") },
            { value: "history", label: t("alerts.tabHistory") },
          ]}
        />
      }
      footer={
        tab === "rules" && canManage && rules && rules.length > 0 ? (
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setEditing("new")}>
              <IconPlus size={14} className="me-1" />
              {t("alerts.add")}
            </Button>
          </div>
        ) : undefined
      }
    >
      {confirmNode}
      {tab === "history" ? (
        <History metrics={metrics} />
      ) : !rules ? (
        <CenterLoader />
      ) : rules.length === 0 ? (
        <EmptyState
          title={t("alerts.emptyTitle")}
          body={t("alerts.emptyBody")}
          action={
            canManage && (
              <div className="flex flex-wrap justify-center gap-2">
                <Button size="sm" onClick={addSuggested} loading={busy}>
                  {t("alerts.addSuggested")}
                </Button>
                <Button size="sm" variant="outline" color="gray" onClick={() => setEditing("new")}>
                  {t("alerts.add")}
                </Button>
              </div>
            )
          }
        />
      ) : (
        <ReadOnly when={!canManage}>
          <div className="flex flex-col">
            {rules.map((r) => (
              <RuleRow
                key={r.id}
                rule={r}
                unit={unitOf(r.metric)}
                scope={scopeOf(r.node_id)}
                canManage={canManage}
                busy={busy}
                onToggle={(on) => toggle(r, on)}
                onEdit={() => setEditing(r)}
                onDelete={() => remove(r)}
              />
            ))}
          </div>
        </ReadOnly>
      )}
      {editing && (
        <RuleEditor
          rule={editing === "new" ? null : editing}
          nodes={nodes}
          metrics={metrics}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
    </Modal>
  );
}
