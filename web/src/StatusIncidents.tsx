import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  addIncidentUpdate,
  compensateIncident,
  deleteIncident,
  deleteIncidentUpdate,
  listIncidents,
  resolveIncident,
  saveIncident,
  type Incident,
  type IncidentKind,
  type IncidentSaved,
  type IncidentServer,
  type IncidentStage,
} from "./api";
import { fmtStamp } from "./format";
import { useAction } from "./hooks";
import { td } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan } from "./role";
import { fromMoment, MomentField, type Moment, toMoment } from "./MomentField";
import {
  Badge,
  Button,
  Checkbox,
  cn,
  Drawer,
  EmptyState,
  IconButton,
  IconCheck,
  IconExternal,
  IconHeart,
  IconPencil,
  IconPlus,
  IconTrash,
  Modal,
  Panel,
  ReadOnly,
  SegmentedControl,
  Select,
  SettingRow,
  Textarea,
  TextInput,
  useConfirm,
} from "./ui";

type Phase = "scheduled" | "active" | "done";

// phaseOf mirrors model.Incident.Phase: a maintenance window ends by itself at its
// close, an incident only when resolved.
function phaseOf(inc: Incident, now: number): Phase {
  if (inc.resolved_at > 0) return "done";
  if (inc.kind !== "maintenance") return "active";
  if (now < inc.starts_at) return "scheduled";
  if (inc.ends_at > 0 && now >= inc.ends_at) return "done";
  return "active";
}

// endedAt mirrors model.Incident.EndedAt: when a finished entry ended, 0 while it
// is not over.
function endedAt(inc: Incident, now: number): number {
  if (phaseOf(inc, now) !== "done") return 0;
  if (inc.resolved_at > 0 && (inc.ends_at === 0 || inc.resolved_at < inc.ends_at || inc.kind !== "maintenance")) {
    return inc.resolved_at;
  }
  return inc.ends_at;
}

// suggestedHours mirrors core.CompensationHours: the entry's length in whole hours,
// rounded up.
function suggestedHours(inc: Incident, now: number): number {
  const end = endedAt(inc, now);
  if (!end) return 0;
  return Math.min(Math.ceil(Math.max(end - inc.starts_at, 1) / 3600), COMPENSATION_HOURS_MAX);
}

const COMPENSATION_HOURS_MAX = 24 * 90;

const PHASE_ORDER: Record<Phase, number> = { active: 0, scheduled: 1, done: 2 };

// A fresh entry starts now; maintenance is moved to its real window by hand.
function blank(kind: IncidentKind): Incident {
  const now = Math.floor(Date.now() / 1000);
  return {
    id: 0,
    kind,
    title: "",
    body: "",
    node_ids: [],
    starts_at: now,
    ends_at: 0,
    resolved_at: 0,
    created_at: 0,
    updated_at: 0,
    hide_in_sub: kind === "maintenance",
    compensated_at: 0,
    compensated_seconds: 0,
    compensated_users: 0,
    snapshot_at: 0,
    updates: [],
  };
}

// StatusIncidents is the operator's list of incidents and maintenance windows shown
// on the public status page, and optionally sent to the users through the bot.
export function StatusIncidents({ statusPath }: { statusPath: string }) {
  const { t } = useTranslation();
  const canManage = useCan("settings.manage");
  const canNotify = useCan("broadcasts.manage");
  const canCompensate = useCan("users.manage");
  const [list, setList] = useState<Incident[] | null>(null);
  const [servers, setServers] = useState<IncidentServer[]>([]);
  const [err, setErr] = useState("");
  const [editor, setEditor] = useState<Incident | null>(null);
  // The title the entry was opened with: the drawer's header names what is being
  // edited, not the field's every keystroke.
  const [editorTitle, setEditorTitle] = useState("");
  const [start, setStart] = useState<Moment>({ date: "", time: "" });
  const [end, setEnd] = useState<Moment>({ date: "", time: "" });
  const [notify, setNotify] = useState(false);
  // Whether the servers were ticked differently: an entry saved without touching
  // them keeps what it named, even if that is every server shown today.
  const [pickedServers, setPickedServers] = useState(false);
  const [resolving, setResolving] = useState<Incident | null>(null);
  const [resolveNotify, setResolveNotify] = useState(false);
  const [resolveNote, setResolveNote] = useState("");
  const [deleting, setDeleting] = useState<Incident | null>(null);
  const [compensating, setCompensating] = useState<Incident | null>(null);
  const [compHours, setCompHours] = useState("");
  const [compNotify, setCompNotify] = useState(true);
  const { busy, run } = useAction();

  const reload = useCallback(() => {
    listIncidents()
      .then((r) => {
        setList(r.incidents ?? []);
        setServers(r.servers ?? []);
        setErr("");
      })
      .catch((e) => setErr(errMessage(e)));
  }, []);
  useEffect(reload, [reload]);

  const open = (inc: Incident) => {
    // The editor holds the servers ticked; the whole service (none named) is every
    // server ticked.
    const named = inc.node_ids ?? [];
    setEditor({ ...inc, node_ids: named.length ? [...named] : servers.map((s) => s.id) });
    setEditorTitle(inc.title);
    setStart(toMoment(inc.starts_at));
    setEnd(toMoment(inc.ends_at));
    setNotify(false);
    setPickedServers(false);
  };
  const patch = (d: Partial<Incident>) => editor && setEditor({ ...editor, ...d });

  // done reports the outcome: saved, and whether the message to the users went out.
  const done = (r: IncidentSaved, ok: string, notified: boolean) => {
    reload();
    if (r.notify_error) {
      const e = r.notify_error;
      const reason = e.code && td(e.code, e.args) !== e.code ? td(e.code, e.args) : e.error;
      notifyError(t("incidents.notifyFailed", { reason }));
    } else {
      notifySuccess(notified ? t("incidents.notifySent") : ok);
    }
  };

  const save = () => {
    if (!editor) return;
    if (editor.node_ids.length === 0) {
      notifyError(t("incidents.pickServer"));
      return;
    }
    // Every server ticked is the whole service: one added later is covered too. An
    // entry whose servers were left alone keeps the list it had.
    const everyServer =
      servers.every((s) => editor.node_ids.includes(s.id)) &&
      editor.node_ids.every((id) => servers.some((s) => s.id === id));
    const stored = list?.find((i) => i.id === editor.id)?.node_ids ?? [];
    const nodeIds = !pickedServers && editor.id ? stored : everyServer ? [] : editor.node_ids;
    const inc: Incident = {
      ...editor,
      node_ids: nodeIds,
      title: editor.title.trim(),
      starts_at: fromMoment(start),
      ends_at: editor.kind === "maintenance" ? fromMoment(end) : 0,
    };
    run(async () => {
      const send = notify && canNotify && editedPhase !== "done";
      const r = await saveIncident(inc, send);
      setEditor(null);
      done(r, t("incidents.saved"), send);
    }).catch((e) => notifyError(errMessage(e)));
  };

  const resolve = () => {
    if (!resolving) return;
    const id = resolving.id;
    run(async () => {
      const r = await resolveIncident(id, resolveNotify && canNotify, resolveNote.trim());
      setResolving(null);
      done(r, t("incidents.resolvedOk"), resolveNotify && canNotify);
    }).catch((e) => notifyError(errMessage(e)));
  };

  const compensate = () => {
    if (!compensating) return;
    const id = compensating.id;
    const hours = Number(compHours);
    if (!Number.isInteger(hours) || hours < 1 || hours > COMPENSATION_HOURS_MAX) {
      notifyError(td("err.compensationHours", { max: COMPENSATION_HOURS_MAX }));
      return;
    }
    run(async () => {
      const r = await compensateIncident(id, hours, compNotify && canNotify);
      setCompensating(null);
      reload();
      notifySuccess(t("incidents.compensatedOk", { users: r.incident.compensated_users }));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const remove = () => {
    if (!deleting) return;
    const id = deleting.id;
    run(async () => {
      await deleteIncident(id);
      setDeleting(null);
      reload();
      notifySuccess(t("incidents.deleted"));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const now = Math.floor(Date.now() / 1000);
  const rows = (list ?? [])
    .map((inc) => ({ inc, phase: phaseOf(inc, now) }))
    // Going on first, then what is planned (soonest first), then the past.
    .sort(
      (a, b) =>
        PHASE_ORDER[a.phase] - PHASE_ORDER[b.phase] ||
        (a.phase === "scheduled" ? a.inc.starts_at - b.inc.starts_at : 0),
    );
  // A server that has left the status page since is still named, by its number:
  // the entry is about it, not about the whole service.
  const serverNames = (ids: number[]) =>
    ids.length === 0
      ? t("incidents.allServers")
      : ids.map((id) => servers.find((s) => s.id === id)?.name ?? `#${id}`).join(", ");
  const when = (inc: Incident) =>
    inc.kind === "maintenance" && inc.ends_at
      ? `${fmtStamp(inc.starts_at)} – ${fmtStamp(inc.ends_at)}`
      : fmtStamp(inc.starts_at);
  const maint = editor?.kind === "maintenance";
  // The entry as last loaded: its course comes from there, so a posted update shows
  // up as soon as the list reloads.
  const current = editor?.id ? list?.find((i) => i.id === editor.id) : undefined;
  // Where the entry stands as edited: one that is over is not announced as news.
  const editedPhase = editor
    ? phaseOf(
        { ...editor, starts_at: fromMoment(start), ends_at: maint ? fromMoment(end) : 0 },
        now,
      )
    : "active";
  // The servers to tick, plus any the entry names that have left the status page
  // since — shown by number so they can be unticked.
  const editorServers = [
    ...servers,
    ...(editor?.node_ids ?? [])
      .filter((id) => !servers.some((s) => s.id === id))
      .map((id) => ({ id, name: `#${id}` })),
  ];

  return (
    <>
      <ReadOnly when={!canManage}>
        <Panel
          title={t("incidents.title")}
          aside={
            <span className="flex items-center gap-1">
              {statusPath && (
                <IconButton
                  nav
                  href={`${window.location.origin}/${statusPath}`}
                  target="_blank"
                  title={t("incidents.openPage")}
                >
                  <IconExternal />
                </IconButton>
              )}
              <IconButton
                variant="filled"
                color="brand"
                title={t("incidents.new")}
                // The servers to tick come with the list.
                disabled={!list || !!err}
                onClick={() => open(blank("incident"))}
              >
                <IconPlus />
              </IconButton>
            </span>
          }
        >
          <SettingRow
            hint={
              <>
                {t("incidents.hint")}
                {!statusPath && (
                  <span className="mt-1 block text-warning">{t("incidents.pageOff")}</span>
                )}
              </>
            }
          />
          {err ? (
            <SettingRow hint={<span className="text-danger">{err}</span>} />
          ) : !list ? null : rows.length === 0 ? (
            <EmptyState title={t("incidents.none")} />
          ) : (
            rows.map(({ inc, phase }) => (
              <div
                key={inc.id}
                className={cn(
                  "flex items-start gap-3 border-t border-gray-100 px-3.5 py-2.5",
                  phase === "done" && "opacity-60",
                )}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge size="xs" color={inc.kind === "maintenance" ? "brand" : "orange"}>
                      {t(inc.kind === "maintenance" ? "incidents.kindMaintenance" : "incidents.kindIncident")}
                    </Badge>
                    <Badge
                      size="xs"
                      color={phase === "active" ? (inc.kind === "maintenance" ? "brand" : "red") : "gray"}
                    >
                      {t(`incidents.phase.${phase}`)}
                    </Badge>
                    <span className="min-w-0 truncate text-[13px] font-medium text-ink">{inc.title}</span>
                  </div>
                  <p className="mt-0.5 truncate text-[11px] text-ink-muted">
                    {when(inc)} · {serverNames(inc.node_ids)}
                  </p>
                  {inc.compensated_at > 0 && (
                    <p className="mt-0.5 truncate text-[11px] text-ink-muted">
                      {t("incidents.compensated", {
                        hours: Math.round(inc.compensated_seconds / 3600),
                        users: inc.compensated_users,
                      })}
                    </p>
                  )}
                </div>
                <span className="flex shrink-0 gap-0.5">
                  {phase === "active" && (
                    <IconButton
                      color="teal"
                      title={t("incidents.resolve")}
                      disabled={busy}
                      onClick={() => {
                        setResolveNotify(false);
                        setResolveNote("");
                        setResolving(inc);
                      }}
                    >
                      <IconCheck size={16} />
                    </IconButton>
                  )}
                  {phase === "done" && canCompensate && inc.compensated_at === 0 && (
                    <IconButton
                      color="teal"
                      title={t("incidents.compensate")}
                      disabled={busy}
                      onClick={() => {
                        setCompHours(String(suggestedHours(inc, now)));
                        setCompNotify(canNotify);
                        setCompensating(inc);
                      }}
                    >
                      <IconHeart size={16} />
                    </IconButton>
                  )}
                  <IconButton title={t("common.edit")} nav onClick={() => open(inc)}>
                    <IconPencil size={16} />
                  </IconButton>
                  <IconButton color="red" title={t("common.delete")} disabled={busy} onClick={() => setDeleting(inc)}>
                    <IconTrash size={16} />
                  </IconButton>
                </span>
              </div>
            ))
          )}
        </Panel>
      </ReadOnly>

      <Drawer
        open={!!editor}
        onClose={() => setEditor(null)}
        title={editor?.id ? editorTitle : t("incidents.new")}
        footer={
          editor ? (
            <div className="flex justify-end gap-2">
              <Button variant="light" color="gray" size="sm" onClick={() => setEditor(null)}>
                {t("common.cancel")}
              </Button>
              <Button size="sm" onClick={save} loading={busy} disabled={!canManage}>
                {t(editor.id ? "common.save" : "common.create")}
              </Button>
            </div>
          ) : undefined
        }
      >
        {editor && (
          <ReadOnly when={!canManage}>
            <div className="flex flex-col gap-4">
              {/* The kind is chosen once: its course's stages depend on it. */}
              {editor.id ? (
                <div>
                  <Badge color={maint ? "brand" : "orange"}>
                    {t(maint ? "incidents.kindMaintenance" : "incidents.kindIncident")}
                  </Badge>
                </div>
              ) : (
                <SegmentedControl
                  fullWidth
                  data={[
                    { value: "incident", label: t("incidents.kindIncident") },
                    { value: "maintenance", label: t("incidents.kindMaintenance") },
                  ]}
                  value={editor.kind}
                  // New maintenance takes its servers out of subscriptions unless told not to.
                  onChange={(v) => patch({ kind: v as IncidentKind, hide_in_sub: v === "maintenance" })}
                />
              )}
              <TextInput
                label={t("incidents.titleLabel")}
                placeholder={t(maint ? "incidents.titlePhMaint" : "incidents.titlePh")}
                value={editor.title}
                onChange={(title) => patch({ title })}
              />
              <Textarea
                label={t("incidents.body")}
                placeholder={t("incidents.bodyPh")}
                rows={4}
                value={editor.body}
                onChange={(body) => patch({ body })}
              />
              <div className="flex flex-col gap-2">
                <span className="text-xs font-medium text-ink">{t("incidents.servers")}</span>
                <div className="flex max-h-52 flex-col gap-1.5 overflow-y-auto rounded-lg border border-gray-200/80 bg-white/50 p-2">
                  {editorServers.map((s) => (
                    <Checkbox
                      key={s.id}
                      checked={editor.node_ids.includes(s.id)}
                      onChange={(c) => {
                        setPickedServers(true);
                        patch({
                          node_ids: c
                            ? [...editor.node_ids, s.id]
                            : editor.node_ids.filter((id) => id !== s.id),
                        });
                      }}
                      label={s.name}
                    />
                  ))}
                </div>
              </div>
              <MomentField
                label={t(maint ? "incidents.startsMaint" : "incidents.starts")}
                value={start}
                onChange={setStart}
              />
              {maint && (
                <MomentField
                  label={t("incidents.ends")}
                  hint={t("incidents.endsHint")}
                  value={end}
                  onChange={setEnd}
                  clearable
                />
              )}
              {/* Only for named servers: the whole service under works stays in. */}
              {maint && !servers.every((sv) => editor.node_ids.includes(sv.id)) && (
                <Checkbox
                  checked={editor.hide_in_sub}
                  onChange={(hide_in_sub) => patch({ hide_in_sub })}
                  label={t("incidents.hideInSub")}
                  hint={t("incidents.hideInSubHint")}
                />
              )}
              {canNotify && editedPhase !== "done" && (
                <Checkbox
                  checked={notify}
                  onChange={setNotify}
                  label={t(editor.id ? "incidents.notifyUpdate" : "incidents.notify")}
                  hint={t("incidents.notifyHint")}
                />
              )}
              {editor.id > 0 && current && (
                <IncidentCourse
                  incident={current}
                  canNotify={canNotify}
                  onDone={(r, ok, notified) => done(r, ok, notified)}
                  onDeleted={() => {
                    reload();
                    notifySuccess(t("incidents.updateDeleted"));
                  }}
                />
              )}
            </div>
          </ReadOnly>
        )}
      </Drawer>

      <Modal
        open={!!resolving}
        onClose={() => setResolving(null)}
        title={t(resolving?.kind === "maintenance" ? "incidents.finishTitle" : "incidents.resolveTitle")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setResolving(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={resolve} loading={busy}>
              {t("incidents.resolve")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm leading-relaxed text-ink-muted">
            {t("incidents.resolveBody", { title: resolving?.title ?? "" })}
          </p>
          <Textarea
            label={t("incidents.note")}
            placeholder={t("incidents.notePh")}
            rows={3}
            value={resolveNote}
            onChange={setResolveNote}
          />
          {canNotify && (
            <Checkbox
              checked={resolveNotify}
              onChange={setResolveNotify}
              label={t("incidents.notify")}
              hint={t("incidents.notifyHint")}
            />
          )}
        </div>
      </Modal>

      <Modal
        open={!!compensating}
        onClose={() => setCompensating(null)}
        title={t("incidents.compensateTitle")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setCompensating(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={compensate} loading={busy}>
              {t("incidents.compensate")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm leading-relaxed text-ink-muted">
            {t("incidents.compensateBody", { title: compensating?.title ?? "" })}
          </p>
          <div>
            <TextInput
              label={t("incidents.compensateHours")}
              type="number"
              inputMode="numeric"
              value={compHours}
              onChange={setCompHours}
            />
            {compensating && (
              <p className="mt-1 text-[11px] text-ink-muted">
                {t("incidents.compensateHoursHint", { hours: suggestedHours(compensating, now) })}
              </p>
            )}
          </div>
          {canNotify && (
            <Checkbox checked={compNotify} onChange={setCompNotify} label={t("incidents.compensateNotify")} />
          )}
        </div>
      </Modal>

      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("incidents.deleteTitle")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button color="red" onClick={remove} loading={busy}>
              {t("common.delete")}
            </Button>
          </div>
        }
      >
        <p className="text-sm leading-relaxed text-ink-muted">
          {t("incidents.deleteBody", { title: deleting?.title ?? "" })}
        </p>
      </Modal>
    </>
  );
}

const STAGES: IncidentStage[] = ["investigating", "identified", "monitoring", "update"];

// IncidentCourse is the "how it is going" feed of an entry: the steps posted so far,
// newest first, and a box to post the next one — optionally sent to the users.
function IncidentCourse({
  incident,
  canNotify,
  onDone,
  onDeleted,
}: {
  incident: Incident;
  canNotify: boolean;
  onDone: (r: IncidentSaved, ok: string, notified: boolean) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const maint = incident.kind === "maintenance";
  const [stage, setStage] = useState<IncidentStage>(maint ? "update" : "investigating");
  const [body, setBody] = useState("");
  const [notify, setNotify] = useState(false);
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();
  const over = phaseOf(incident, Math.floor(Date.now() / 1000)) === "done";
  const label = (s: IncidentStage) =>
    s === "resolved"
      ? t(maint ? "incidents.stage.done" : "incidents.stage.resolved")
      : t(`incidents.stage.${s}`);

  const post = () =>
    run(async () => {
      const send = notify && canNotify && !over;
      const r = await addIncidentUpdate(incident.id, maint ? "update" : stage, body.trim(), send);
      setBody("");
      setNotify(false);
      onDone(r, t("incidents.updatePosted"), send);
    });
  const remove = async (id: number) => {
    const ok = await confirm({
      title: t("incidents.deleteUpdateTitle"),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!ok) return;
    run(async () => {
      await deleteIncidentUpdate(id);
      onDeleted();
    });
  };

  const updates = [...(incident.updates ?? [])].reverse();
  return (
    <div className="flex flex-col gap-2.5 border-t border-gray-100 pt-4">
      {confirmNode}
      <span className="text-xs font-medium text-ink">{t("incidents.course")}</span>
      {updates.length > 0 && (
        <ol className="flex flex-col gap-2">
          {updates.map((u) => (
            <li key={u.id} className="flex items-start gap-2 rounded-lg bg-gray-50 px-2.5 py-2">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2 text-xs">
                  <span className="font-semibold text-ink">{label(u.stage)}</span>
                  <span className="text-ink-muted">{fmtStamp(u.created_at)}</span>
                </div>
                <p className="mt-0.5 whitespace-pre-line break-words text-[13px] text-ink" dir="auto">
                  {u.body}
                </p>
              </div>
              <IconButton
                compact
                color="red"
                title={t("incidents.deleteUpdate")}
                disabled={busy}
                onClick={() => remove(u.id)}
              >
                <IconTrash size={14} />
              </IconButton>
            </li>
          ))}
        </ol>
      )}
      {!maint && (
        <Select
          label={t("incidents.stageLabel")}
          data={STAGES.map((s) => ({ value: s, label: label(s) }))}
          value={stage}
          onChange={(v) => setStage(v as IncidentStage)}
        />
      )}
      <Textarea
        label={t("incidents.updateBody")}
        placeholder={t(maint ? "incidents.updatePhMaint" : "incidents.updatePh")}
        rows={3}
        value={body}
        onChange={setBody}
      />
      {canNotify && !over && (
        <Checkbox
          checked={notify}
          onChange={setNotify}
          label={t("incidents.notifyPost")}
          hint={t("incidents.notifyHint")}
        />
      )}
      <div className="flex justify-end">
        <Button size="sm" variant="light" onClick={post} loading={busy} disabled={!body.trim()}>
          {t("incidents.post")}
        </Button>
      </div>
    </div>
  );
}
