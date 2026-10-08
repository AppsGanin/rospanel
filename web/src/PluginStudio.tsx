// PluginStudio is where a plugin is written in the panel: the rule builder or the
// code, its tests and a trial run in the sandbox, then the ordinary install (the
// consent screen is the plugins page's own) or a download to carry on elsewhere.
//
// Every edit is saved to the draft as it is made; nothing reaches the running
// panel until the install.
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import {
  checkPluginDraft,
  type DraftCheck,
  type DraftFile,
  type DraftRun,
  type DraftTestResult,
  type DraftView,
  getPluginDraft,
  inspectPluginDraft,
  type PluginInspection,
  pluginDraftDownloadURL,
  type Rule,
  type RuleAction,
  type RuleActionType,
  type RuleCondition,
  type RuleOp,
  type RuleSpec,
  runPluginDraft,
  savePluginDraft,
  testPluginDraft,
} from "./api";
import { CronPicker, detectPreset, buildCron } from "./CronPicker";
import { langOf } from "./codeLang";
import { slugKey, td } from "./i18n";
import { errMessage, notifyError } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  Checkbox,
  cn,
  Dropdown,
  DropdownItem,
  EmptyState,
  IconButton,
  IconClose,
  IconPlus,
  IconTrash,
  Modal,
  SegmentedControl,
  Select,
  Spinner,
  Textarea,
  TextInput,
  useConfirm,
  useLockBody,
} from "./ui";

const CodeEditor = lazy(() => import("./CodeEditor"));

type Tab = "builder" | "code" | "tests" | "run";
type SaveState = "saved" | "saving" | "dirty" | "failed";

const SAVE_DELAY = 800;

export function PluginStudio({
  draftId,
  onClose,
  onInstall,
}: {
  draftId: number;
  onClose: () => void;
  onInstall: (review: PluginInspection) => void;
}) {
  const { t } = useTranslation();
  const [view, setView] = useState<DraftView | null>(null);
  const [tab, setTab] = useState<Tab>("code");
  const [save, setSave] = useState<SaveState>("saved");
  const [check, setCheck] = useState<DraftCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [installing, setInstalling] = useState(false);
  const pending = useRef<{ spec?: RuleSpec; files?: DraftFile[] } | null>(null);
  const timer = useRef<number | undefined>(undefined);
  // No Escape to close: it is the key that dismisses the editor's completion and
  // the dialogs on top, and a workspace closed by a stray key loses its place.
  useLockBody(true);

  useEffect(() => {
    getPluginDraft(draftId)
      .then((v) => {
        setView(v);
        setTab(v.draft.mode === "builder" ? "builder" : "code");
      })
      .catch((e) => {
        notifyError(errMessage(e));
        onClose();
      });
  }, [draftId, onClose]);

  // flush sends what waits to be saved now: before leaving, testing, installing.
  const flush = useCallback(async () => {
    window.clearTimeout(timer.current);
    const change = pending.current;
    if (!change) return true;
    pending.current = null;
    setSave("saving");
    try {
      const v = await savePluginDraft(draftId, change);
      // What was typed while the save travelled stays: the answer brings the rest
      // (the files the rules made, the problems, the draft's name).
      setView((cur) =>
        cur ? { ...v, spec: change.spec ? cur.spec : v.spec, files: change.files ? cur.files : v.files } : v,
      );
      setSave("saved");
      return true;
    } catch (e) {
      setSave("failed");
      notifyError(errMessage(e));
      return false;
    }
  }, [draftId]);

  const queue = (change: { spec?: RuleSpec; files?: DraftFile[] }) => {
    pending.current = { ...pending.current, ...change };
    setSave("dirty");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(flush, SAVE_DELAY);
  };
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const runCheck = async () => {
    if (!(await flush())) return;
    setChecking(true);
    try {
      setCheck(await checkPluginDraft(draftId));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setChecking(false);
    }
  };

  const install = async () => {
    if (!(await flush())) return;
    setInstalling(true);
    try {
      const c = await checkPluginDraft(draftId);
      if (!c.ok) {
        setCheck(c);
        return;
      }
      onInstall(await inspectPluginDraft(draftId));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setInstalling(false);
    }
  };

  const toCode = async () => {
    await flush();
    try {
      const v = await savePluginDraft(draftId, { mode: "code" });
      setView(v);
      setTab("code");
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  const d = view?.draft;
  const builder = d?.mode === "builder";
  const tabs = [
    ...(builder ? [{ value: "builder", label: t("studio.tabBuilder") }] : []),
    { value: "code", label: t("studio.tabCode") },
    { value: "tests", label: t("studio.tabTests") },
    { value: "run", label: t("studio.tabRun") },
  ];

  return createPortal(
    <div className="fixed inset-0 z-200 flex flex-col bg-white animate-fade-in">
      <header className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-gray-100 px-4 py-2.5">
        <IconButton title={t("common.close")} onClick={() => flush().then(onClose)}>
          <IconClose size={18} />
        </IconButton>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-base font-bold text-ink">{d?.name || t("studio.untitled")}</span>
            {d && (
              <Badge color={builder ? "teal" : "gray"} size="xs">
                {builder ? t("studio.modeBuilder") : t("studio.modeCode")}
              </Badge>
            )}
          </div>
          <div className="flex items-center gap-2 text-[11px] text-ink-muted">
            {d?.plugin_id && (
              <span className="font-mono">
                {d.plugin_id}@{d.version}
              </span>
            )}
            <SaveBadge state={save} />
          </div>
        </div>
        {view && (
          // One height with the buttons beside it (h-8, a small button's).
          <div className="order-last w-full sm:order-none sm:w-auto [&>div]:h-8 [&_button]:py-0">
            <SegmentedControl data={tabs} value={tab} onChange={(v) => setTab(v as Tab)} nav />
          </div>
        )}
        <div className="flex items-center gap-2">
          <Button size="sm" className="h-8" variant="light" color="gray" loading={checking} onClick={runCheck}>
            {t("studio.check")}
          </Button>
          <Dropdown
            width={220}
            trigger={
              <Button size="sm" className="h-8" variant="light" color="gray">
                {t("studio.download")}
              </Button>
            }
          >
            <DropdownItem href={pluginDraftDownloadURL(draftId, "package")}>{t("studio.downloadPackage")}</DropdownItem>
            <DropdownItem href={pluginDraftDownloadURL(draftId, "sources")}>{t("studio.downloadSources")}</DropdownItem>
          </Dropdown>
          <Button size="sm" className="h-8" loading={installing} onClick={install}>
            {t("studio.install")}
          </Button>
        </div>
      </header>

      <div className="min-h-0 flex-1">
        {!view ? (
          <CenterLoader />
        ) : tab === "builder" && view.spec ? (
          <BuilderView
            spec={view.spec}
            events={view.events}
            problems={view.problems}
            onChange={(spec) => {
              setView((cur) => (cur ? { ...cur, spec } : cur));
              queue({ spec });
            }}
          />
        ) : tab === "code" ? (
          <CodeView
            files={view.files}
            builder={builder}
            onToCode={toCode}
            onChange={(files) => {
              setView((cur) => (cur ? { ...cur, files } : cur));
              queue({ files });
            }}
          />
        ) : tab === "tests" ? (
          <TestsView
            draftId={draftId}
            files={view.files}
            builder={builder}
            flush={flush}
            onAddTest={(files) => {
              setView((cur) => (cur ? { ...cur, files } : cur));
              queue({ files });
            }}
          />
        ) : (
          <RunView draftId={draftId} files={view.files} events={view.events} flush={flush} />
        )}
      </div>

      {check && <CheckDialog check={check} onClose={() => setCheck(null)} />}
    </div>,
    document.body,
  );
}

function SaveBadge({ state }: { state: SaveState }) {
  const { t } = useTranslation();
  if (state === "saving" || state === "dirty")
    return (
      <span className="flex items-center gap-1">
        <Spinner size={10} /> {t("studio.saving")}
      </span>
    );
  if (state === "failed") return <span className="text-danger">{t("studio.saveFailed")}</span>;
  return <span>{t("studio.saved")}</span>;
}

// --- the rule builder ---

const OPS: RuleOp[] = ["eq", "ne", "contains", "gt", "lt", "empty", "not_empty"];
const ACTIONS: RuleActionType[] = ["telegram", "discord", "http", "extend", "enable", "disable", "tag", "untag", "log"];
const USER_ACTIONS: RuleActionType[] = ["extend", "enable", "disable", "tag", "untag"];
const FIELDS = [
  "user.id",
  "user.name",
  "user.status",
  "user.plan_id",
  "user.lang",
  "user.telegram_id",
  "user.external_id",
  "data.source",
  "data.amount_rub",
  "data.plan",
  "event",
];

function BuilderView({
  spec,
  events,
  problems,
  onChange,
}: {
  spec: RuleSpec;
  events: string[];
  problems?: string[];
  onChange: (s: RuleSpec) => void;
}) {
  const { t } = useTranslation();
  const set = (patch: Partial<RuleSpec>) => onChange({ ...spec, ...patch });
  const setRule = (i: number, r: Rule) => set({ rules: spec.rules.map((x, j) => (j === i ? r : x)) });
  const eventOptions = useMemo(
    () => events.map((k) => ({ value: k, label: `${td(`webhookEvent.${slugKey(k)}`)} · ${k}` })),
    [events],
  );

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-5 px-4 py-5">
        {problems && problems.length > 0 && (
          <div className="danger-tint rounded-xl px-4 py-3 text-xs">
            <p className="mb-1 font-semibold text-danger">{t("studio.problems")}</p>
            <ul className="list-disc pl-4 font-mono text-[11px] text-ink">
              {problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </div>
        )}

        <section className="grid gap-3 sm:grid-cols-2">
          <TextInput label={t("studio.name")} value={spec.name} onChange={(v) => set({ name: v })} />
          <div className="grid grid-cols-[1fr_7rem] gap-3">
            <TextInput label={t("studio.pluginId")} value={spec.id} onChange={(v) => set({ id: v })} mono />
            <TextInput label={t("studio.version")} value={spec.version} onChange={(v) => set({ version: v })} mono />
          </div>
          <div className="sm:col-span-2">
            <Textarea
              label={t("studio.description")}
              value={spec.description ?? ""}
              onChange={(v) => set({ description: v })}
              rows={2}
            />
          </div>
        </section>

        {spec.rules.map((r, i) => (
          <RuleCard
            // biome-ignore lint/suspicious/noArrayIndexKey: rules have no id of their own; order is their identity
            key={i}
            n={i + 1}
            rule={r}
            eventOptions={eventOptions}
            onChange={(nr) => setRule(i, nr)}
            onRemove={spec.rules.length > 1 ? () => set({ rules: spec.rules.filter((_, j) => j !== i) }) : undefined}
          />
        ))}

        <Button
          variant="light"
          onClick={() =>
            set({ rules: [...spec.rules, { event: "user.created", actions: [{ type: "log", text: "{{user.name}}" }] }] })
          }
        >
          <IconPlus size={14} /> {t("studio.addRule")}
        </Button>

        <p className="text-xs leading-relaxed text-ink-muted">{braces(t("studio.placeholdersHint"))}</p>
      </div>
    </div>
  );
}

function RuleCard({
  n,
  rule,
  eventOptions,
  onChange,
  onRemove,
}: {
  n: number;
  rule: Rule;
  eventOptions: { value: string; label: string }[];
  onChange: (r: Rule) => void;
  onRemove?: () => void;
}) {
  const { t } = useTranslation();
  const set = (patch: Partial<Rule>) => onChange({ ...rule, ...patch });
  const scheduled = rule.schedule !== undefined && rule.event === undefined;
  const conds = rule.conditions ?? [];

  return (
    <section className="rounded-2xl border border-gray-200">
      <div className="flex items-center gap-2 border-b border-gray-100 px-4 py-2.5">
        <span className="text-xs font-bold text-ink-muted">{t("studio.rule", { n })}</span>
        <input
          className="min-w-0 flex-1 bg-transparent text-sm font-semibold text-ink outline-none placeholder:text-gray-400"
          placeholder={t("studio.ruleName")}
          aria-label={t("studio.ruleName")}
          value={rule.name ?? ""}
          onChange={(e) => set({ name: e.target.value })}
        />
        {onRemove && (
          <IconButton title={t("studio.removeRule")} color="red" variant="subtle" onClick={onRemove}>
            <IconTrash size={15} />
          </IconButton>
        )}
      </div>

      <div className="flex flex-col gap-4 px-4 py-4">
        <Step label={t("studio.when")}>
          <div>
          <SegmentedControl
            size="xs"
            data={[
              { value: "event", label: t("studio.onEvent") },
              { value: "schedule", label: t("studio.onSchedule") },
            ]}
            value={scheduled ? "schedule" : "event"}
            onChange={(v) =>
              v === "schedule"
                ? set({
                    event: undefined,
                    schedule: "0 9 * * *",
                    actions: rule.actions.filter((a) => !USER_ACTIONS.includes(a.type)),
                  })
                : set({ schedule: undefined, event: "user.created" })
            }
          />
          </div>
          {scheduled ? (
            <CronPicker
              value={detectPreset(rule.schedule ?? "")}
              onChange={(s) => set({ schedule: buildCron(s) })}
            />
          ) : (
            <Select searchable value={rule.event ?? ""} onChange={(v) => set({ event: v })} data={eventOptions} />
          )}
        </Step>

        <Step
          label={t("studio.if")}
          aside={
            conds.length > 1 && (
              <SegmentedControl
                size="xs"
                data={[
                  { value: "all", label: t("studio.matchAll") },
                  { value: "any", label: t("studio.matchAny") },
                ]}
                value={rule.match ?? "all"}
                onChange={(v) => set({ match: v as "all" | "any" })}
              />
            )
          }
        >
          {conds.length === 0 && <p className="text-xs text-ink-muted">{t("studio.always")}</p>}
          {conds.map((c, j) => (
            <ConditionRow
              // biome-ignore lint/suspicious/noArrayIndexKey: conditions have no id; position is theirs
              key={j}
              cond={c}
              onChange={(nc) => set({ conditions: conds.map((x, k) => (k === j ? nc : x)) })}
              onRemove={() => set({ conditions: conds.filter((_, k) => k !== j) })}
            />
          ))}
          <div>
            <Button
              size="xs"
              variant="subtle"
              onClick={() => set({ conditions: [...conds, { field: "user.lang", op: "eq", value: "" }] })}
            >
              <IconPlus size={12} /> {t("studio.addCondition")}
            </Button>
          </div>
        </Step>

        <Step label={t("studio.then")}>
          {rule.actions.map((a, j) => (
            <ActionRow
              // biome-ignore lint/suspicious/noArrayIndexKey: actions have no id; position is theirs
              key={j}
              action={a}
              scheduled={scheduled}
              onChange={(na) => set({ actions: rule.actions.map((x, k) => (k === j ? na : x)) })}
              onRemove={rule.actions.length > 1 ? () => set({ actions: rule.actions.filter((_, k) => k !== j) }) : undefined}
            />
          ))}
          <div>
            <Button
              size="xs"
              variant="subtle"
              onClick={() => set({ actions: [...rule.actions, { type: "log", text: "" }] })}
            >
              <IconPlus size={12} /> {t("studio.addAction")}
            </Button>
          </div>
        </Step>
      </div>
    </section>
  );
}

function Step({ label, aside, children }: { label: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-bold tracking-wide text-ink-muted uppercase">{label}</span>
        {aside}
      </div>
      {children}
    </div>
  );
}

function ConditionRow({
  cond,
  onChange,
  onRemove,
}: {
  cond: RuleCondition;
  onChange: (c: RuleCondition) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const listId = useMemo(() => `fields-${Math.random().toString(36).slice(2)}`, []);
  const unary = cond.op === "empty" || cond.op === "not_empty";
  return (
    <div className="grid grid-cols-[1fr_auto] items-center gap-2 sm:grid-cols-[1.2fr_1fr_1.2fr_auto]">
      <div className="col-span-2 sm:col-span-1">
        <input
          list={listId}
          aria-label={t("studio.field")}
          className="w-full rounded-md border border-gray-300 bg-white px-3 py-1.5 font-mono text-[13px] text-ink outline-none placeholder:text-gray-400 focus:border-brand-500 focus:ring-2 focus:ring-brand-500/25"
          value={cond.field}
          onChange={(e) => onChange({ ...cond, field: e.target.value })}
        />
        <datalist id={listId}>
          {FIELDS.map((f) => (
            <option key={f} value={f} />
          ))}
        </datalist>
      </div>
      <Select
        value={cond.op}
        onChange={(v) => onChange({ ...cond, op: v as RuleOp })}
        data={OPS.map((o) => ({ value: o, label: t(`studio.op.${o}`) }))}
      />
      {unary ? (
        <span className="hidden sm:block" />
      ) : (
        <TextInput ariaLabel={t("studio.value")} value={cond.value ?? ""} onChange={(v) => onChange({ ...cond, value: v })} />
      )}
      <IconButton title={t("common.delete")} variant="subtle" color="gray" onClick={onRemove}>
        <IconTrash size={14} />
      </IconButton>
    </div>
  );
}

function ActionRow({
  action: a,
  scheduled,
  onChange,
  onRemove,
}: {
  action: RuleAction;
  scheduled: boolean;
  onChange: (a: RuleAction) => void;
  onRemove?: () => void;
}) {
  const { t } = useTranslation();
  const set = (patch: Partial<RuleAction>) => onChange({ ...a, ...patch });
  const types = ACTIONS.filter((x) => !scheduled || !USER_ACTIONS.includes(x));
  return (
    <div className="flex flex-col gap-2 rounded-xl bg-gray-50 p-3">
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <Select
            value={a.type}
            onChange={(v) => onChange({ type: v as RuleActionType, ...defaultsFor(v as RuleActionType) })}
            data={types.map((x) => ({ value: x, label: t(`studio.action.${x}`) }))}
          />
        </div>
        {onRemove && (
          <IconButton title={t("common.delete")} variant="subtle" color="gray" onClick={onRemove}>
            <IconTrash size={14} />
          </IconButton>
        )}
      </div>
      {a.type === "telegram" && (
        <>
          <TextInput
            label={t("studio.chat")}
            value={a.chat ?? ""}
            onChange={(v) => set({ chat: v })}
            placeholder="-1001234567890 · {{user.telegram_id}}"
            mono
          />
          <Textarea label={t("studio.text")} value={a.text ?? ""} onChange={(v) => set({ text: v })} rows={3} />
          <p className="text-[11px] text-ink-muted">{braces(t("studio.telegramHint"))}</p>
        </>
      )}
      {(a.type === "discord" || a.type === "log") && (
        <Textarea label={t("studio.text")} value={a.text ?? ""} onChange={(v) => set({ text: v })} rows={3} />
      )}
      {a.type === "discord" && <p className="text-[11px] text-ink-muted">{t("studio.discordHint")}</p>}
      {a.type === "http" && (
        <>
          <div className="grid grid-cols-[7rem_1fr] gap-2">
            <Select
              value={(a.method ?? "POST").toUpperCase()}
              onChange={(v) => set({ method: v })}
              data={["POST", "GET", "PUT", "PATCH", "DELETE"].map((m) => ({ value: m, label: m }))}
            />
            <TextInput
              ariaLabel="URL"
              value={a.url ?? ""}
              onChange={(v) => set({ url: v })}
              placeholder="https://example.com/hook?user={{user.id}}"
              mono
            />
          </div>
          <Textarea
            label={t("studio.body")}
            value={a.body ?? ""}
            onChange={(v) => set({ body: v })}
            rows={3}
            mono
            placeholder={t("studio.bodyPlaceholder")}
          />
          <Checkbox checked={!!a.auth} onChange={(v) => set({ auth: v })} label={t("studio.httpAuth")} />
        </>
      )}
      {a.type === "extend" && (
        <TextInput
          label={t("studio.days")}
          type="number"
          value={String(a.days ?? 1)}
          onChange={(v) => set({ days: Math.max(0, Number.parseInt(v, 10) || 0) })}
        />
      )}
      {(a.type === "tag" || a.type === "untag") && (
        <TextInput label={t("studio.tag")} value={a.tag ?? ""} onChange={(v) => set({ tag: v })} />
      )}
    </div>
  );
}

// braces turns the dictionaries' [[path]] into the {{path}} a rule writes: written
// as is, i18next would read it as its own placeholder and blank it.
const braces = (s: string) => s.split("[[").join("{{").split("]]").join("}}");

function defaultsFor(type: RuleActionType): Partial<RuleAction> {
  switch (type) {
    case "telegram":
      return { chat: "", text: "" };
    case "http":
      return { method: "POST", url: "" };
    case "extend":
      return { days: 7 };
    case "tag":
    case "untag":
      return { tag: "" };
    case "discord":
    case "log":
      return { text: "" };
  }
  return {};
}

// --- code ---

const NEW_TEST = `// plugin.event / plugin.call reach the plugin; mock.http and mock.api answer what it asks.
test("handles a new user", () => {
  plugin.event("user.created", { id: 1, name: "Ann" });
});
`;

function CodeView({
  files,
  builder,
  onChange,
  onToCode,
}: {
  files: DraftFile[];
  builder: boolean;
  onChange: (files: DraftFile[]) => void;
  onToCode: () => void;
}) {
  const { t } = useTranslation();
  const { confirm, confirmNode } = useConfirm();
  const [current, setCurrent] = useState(() => (files.some((f) => f.path === "main.js") ? "main.js" : (files[0]?.path ?? "")));
  const [adding, setAdding] = useState<string | null>(null);
  const file = files.find((f) => f.path === current);

  const setText = (text: string) => onChange(files.map((f) => (f.path === current ? { ...f, text } : f)));
  const add = () => {
    const p = (adding ?? "").trim();
    setAdding(null);
    if (!p) return;
    if (files.some((f) => f.path === p)) {
      setCurrent(p);
      return;
    }
    onChange([...files, { path: p, text: "" }].sort((a, b) => a.path.localeCompare(b.path)));
    setCurrent(p);
  };
  const remove = async (path: string) => {
    if (!(await confirm({ title: t("studio.removeFile", { path }), danger: true, confirmLabel: t("common.delete") }))) return;
    const rest = files.filter((f) => f.path !== path);
    onChange(rest);
    if (current === path) setCurrent(rest[0]?.path ?? "");
  };
  const toCode = async () => {
    if (await confirm({ title: t("studio.toCodeTitle"), body: t("studio.toCodeBody"), confirmLabel: t("studio.toCode") })) onToCode();
  };

  return (
    <div className="flex h-full min-h-0 flex-col sm:flex-row">
      <aside className="flex shrink-0 flex-col border-b border-gray-100 sm:w-56 sm:border-r sm:border-b-0">
        <div className="flex max-h-40 flex-col overflow-y-auto py-1 sm:max-h-none sm:flex-1">
          {files.map((f) => (
            <div
              key={f.path}
              className={cn(
                "group flex items-center gap-1 px-2",
                f.path === current ? "accent-tint" : "accent-tint-hover",
              )}
            >
              <button
                type="button"
                className="min-w-0 flex-1 truncate py-1.5 text-left font-mono text-xs text-ink"
                onClick={() => setCurrent(f.path)}
              >
                {f.path}
              </button>
              {!builder && (
                <button
                  type="button"
                  title={t("common.delete")}
                  className="text-gray-400 opacity-0 group-hover:opacity-100 hover:text-danger focus:opacity-100"
                  onClick={() => remove(f.path)}
                >
                  <IconTrash size={13} />
                </button>
              )}
            </div>
          ))}
        </div>
        {!builder && (
          <div className="border-t border-gray-100 p-2">
            {adding === null ? (
              <Button size="xs" variant="subtle" fullWidth onClick={() => setAdding("")}>
                <IconPlus size={12} /> {t("studio.addFile")}
              </Button>
            ) : (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  add();
                }}
              >
                <TextInput
                  ariaLabel={t("studio.addFile")}
                  value={adding}
                  onChange={setAdding}
                  placeholder="migrations/0002_x.sql"
                  mono
                  autoFocus
                />
              </form>
            )}
          </div>
        )}
      </aside>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {builder && (
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-2 text-xs text-ink-muted">
            {t("studio.builtCode")}
            <Button size="xs" variant="light" onClick={toCode}>
              {t("studio.toCode")}
            </Button>
          </div>
        )}
        <div className="min-h-0 flex-1">
          {!file ? (
            <EmptyState title={t("studio.noFile")} />
          ) : file.base64 !== undefined ? (
            <EmptyState title={t("studio.binaryFile")} />
          ) : (
            <Suspense fallback={<CenterLoader />}>
              <CodeEditor
                value={file.text ?? ""}
                onChange={builder ? undefined : setText}
                lang={langOf(file.path)}
                readOnly={builder}
              />
            </Suspense>
          )}
        </div>
      </div>
      {confirmNode}
    </div>
  );
}

// --- tests ---

function TestsView({
  draftId,
  files,
  builder,
  flush,
  onAddTest,
}: {
  draftId: number;
  files: DraftFile[];
  builder: boolean;
  flush: () => Promise<boolean>;
  onAddTest: (files: DraftFile[]) => void;
}) {
  const { t } = useTranslation();
  const [running, setRunning] = useState(false);
  const [res, setRes] = useState<{ results: DraftTestResult[]; output: string; error?: string } | null>(null);
  const has = files.some((f) => f.path === "test.js");

  const run = async () => {
    if (!(await flush())) return;
    setRunning(true);
    try {
      setRes(await testPluginDraft(draftId));
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRunning(false);
    }
  };

  if (!has)
    return (
      <EmptyState
        title={t("studio.noTests")}
        body={t("studio.noTestsHint")}
        action={
          !builder && (
            <Button size="sm" onClick={() => onAddTest([...files, { path: "test.js", text: NEW_TEST }])}>
              {t("studio.addTests")}
            </Button>
          )
        }
      />
    );

  const passed = res?.results.filter((r) => r.ok).length ?? 0;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-4 px-4 py-5">
        <div className="flex flex-wrap items-center gap-3">
          <Button loading={running} onClick={run}>
            {t("studio.runTests")}
          </Button>
          {res && !res.error && (
            <span className={cn("text-sm font-semibold", passed === res.results.length ? "text-success" : "text-danger")}>
              {t("studio.testsPassed", { n: passed, total: res.results.length })}
            </span>
          )}
          <span className="text-xs text-ink-muted">{t("studio.testsHint")}</span>
        </div>
        {res?.error && <ErrorBox text={res.error} />}
        {res && res.results.length > 0 && (
          <ul className="flex flex-col divide-y divide-gray-100 rounded-xl border border-gray-200">
            {res.results.map((r) => (
              <li key={r.name} className="px-3.5 py-2.5 text-sm">
                <span className={r.ok ? "text-success" : "text-danger"}>{r.ok ? "✓" : "✗"}</span>{" "}
                <span className="text-ink">{r.name}</span>
                {!r.ok && (
                  <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px] text-ink-muted">
                    {r.error}
                    {r.stack ? `\n${r.stack}` : ""}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        )}
        {res?.output.trim() && <Pre title={t("studio.output")} text={res.output} />}
      </div>
    </div>
  );
}

// --- trial run ---

const SAMPLE_USER = { id: 1, name: "test-user", status: "active", enabled: true, plan_id: 0, telegram_id: 0, lang: "ru" };

function RunView({
  draftId,
  files,
  events,
  flush,
}: {
  draftId: number;
  files: DraftFile[];
  events: string[];
  flush: () => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<"event" | "call">("event");
  // The first event the plugin takes, when it takes any.
  const [event, setEvent] = useState(() => {
    try {
      const m = JSON.parse(files.find((f) => f.path === "plugin.json")?.text ?? "{}");
      return (m.provides?.events?.[0] as string | undefined) ?? "user.created";
    } catch {
      return "user.created";
    }
  });
  const [data, setData] = useState(() => JSON.stringify(SAMPLE_USER, null, 2));
  const exportsOf = useMemo(() => {
    const main = files.find((f) => f.path === "main.js")?.text ?? "";
    return [...main.matchAll(/export\s+(?:async\s+)?function\s+([A-Za-z_$][\w$]*)/g)].map((m) => m[1]);
  }, [files]);
  const [fn, setFn] = useState("");
  const [arg, setArg] = useState("");
  const [realHTTP, setRealHTTP] = useState(false);
  const [realAPI, setRealAPI] = useState(false);
  const [running, setRunning] = useState(false);
  const [res, setRes] = useState<DraftRun | null>(null);
  const target = fn || exportsOf[0] || "";

  const run = async () => {
    let parsed: unknown;
    const raw = kind === "event" ? data : arg;
    if (raw.trim()) {
      try {
        parsed = JSON.parse(raw);
      } catch {
        notifyError(t("studio.badJSON"));
        return;
      }
    }
    if (!(await flush())) return;
    setRunning(true);
    try {
      setRes(
        await runPluginDraft(draftId, {
          ...(kind === "event" ? { event, data: parsed ?? {} } : { export: target, arg: parsed }),
          real_http: realHTTP,
          real_api: realAPI,
        }),
      );
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-4 px-4 py-5">
        <div>
          <SegmentedControl
            data={[
              { value: "event", label: t("studio.runEvent") },
              { value: "call", label: t("studio.runCall") },
            ]}
            value={kind}
            onChange={(v) => setKind(v as "event" | "call")}
            nav
          />
        </div>
        {kind === "event" ? (
          <>
            <Select
              searchable
              value={event}
              onChange={setEvent}
              data={events.map((k) => ({ value: k, label: `${td(`webhookEvent.${slugKey(k)}`)} · ${k}` }))}
            />
            <JSONField label={t("studio.eventData")} value={data} onChange={setData} />
          </>
        ) : (
          <>
            <Select
              value={target}
              onChange={setFn}
              data={exportsOf.map((e) => ({ value: e, label: e }))}
              placeholder={t("studio.noExports")}
            />
            <JSONField label={t("studio.argument")} value={arg} onChange={setArg} />
          </>
        )}
        <div className="flex flex-col gap-2">
          <Checkbox checked={realHTTP} onChange={setRealHTTP} label={t("studio.realHTTP")} hint={t("studio.realHTTPHint")} />
          <Checkbox checked={realAPI} onChange={setRealAPI} label={t("studio.realAPI")} hint={t("studio.realAPIHint")} />
        </div>
        <div>
          <Button loading={running} disabled={kind === "call" && !target} onClick={run}>
            {t("studio.run")}
          </Button>
        </div>

        {res && (
          <div className="flex flex-col gap-3">
            {res.error ? (
              <ErrorBox text={res.error} />
            ) : (
              <Pre title={t("studio.result")} text={res.result === undefined || res.result === null ? "null" : JSON.stringify(res.result, null, 2)} />
            )}
            {res.calls.length > 0 && (
              <div>
                <p className="mb-1.5 text-xs font-semibold text-ink">{t("studio.calls")}</p>
                <ul className="flex flex-col divide-y divide-gray-100 rounded-xl border border-gray-200">
                  {res.calls.map((c, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: a log of calls, in order
                    <li key={i} className="px-3.5 py-2 text-xs">
                      <span className="font-mono">
                        <Badge size="xs" color={c.kind === "api" ? "teal" : "gray"}>
                          {c.kind === "api" ? "panel.api" : "http"}
                        </Badge>{" "}
                        {c.method} {c.url}
                      </span>
                      {c.body && (
                        <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] text-ink-muted">
                          {c.body}
                        </pre>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {res.logs.length > 0 && (
              <Pre
                title={t("studio.log")}
                text={res.logs.map((l) => `${l.level.padEnd(5)} ${l.msg}`).join("\n")}
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function JSONField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <p className="mb-1 text-xs font-semibold text-ink">{label}</p>
      <div className="h-56 overflow-hidden rounded-lg border border-gray-300">
        <Suspense fallback={<CenterLoader />}>
          <CodeEditor value={value} onChange={onChange} lang="json" />
        </Suspense>
      </div>
    </div>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <pre className="danger-tint overflow-x-auto whitespace-pre-wrap break-words rounded-xl px-3.5 py-3 font-mono text-[11px] text-danger">
      {text}
    </pre>
  );
}

function Pre({ title, text }: { title: string; text: string }) {
  return (
    <div>
      <p className="mb-1.5 text-xs font-semibold text-ink">{title}</p>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-gray-50 px-3.5 py-3 font-mono text-[11px] text-ink">
        {text}
      </pre>
    </div>
  );
}

function CheckDialog({ check, onClose }: { check: DraftCheck; onClose: () => void }) {
  const { t } = useTranslation();
  const m = check.manifest;
  return (
    <Modal open onClose={onClose} title={check.ok ? t("studio.checkOK") : t("studio.checkFailed")}>
      {!check.ok ? (
        <ul className="list-disc pl-4 font-mono text-[11px] text-ink">
          {(check.problems ?? []).map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      ) : (
        <div className="flex flex-col gap-3 text-xs text-ink">
          <p>
            <span className="font-semibold">{t("studio.checkPerms")}</span>{" "}
            {(m?.permissions ?? []).join(", ") || t("studio.none")}
          </p>
          <p>
            <span className="font-semibold">{t("studio.checkNet")}</span> {(m?.net ?? []).join(", ") || t("studio.none")}
          </p>
          <p>
            <span className="font-semibold">{t("studio.checkExports")}</span> {(check.exports ?? []).join(", ")}
          </p>
          <p className="text-ink-muted">{t("studio.checkSize", { kb: Math.ceil(check.size / 1024) })}</p>
        </div>
      )}
    </Modal>
  );
}
