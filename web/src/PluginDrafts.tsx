// Drafts: the plugins being written in the panel (see PluginStudio). A list on the
// plugins page, and the dialog that starts one — from the rule builder, from the
// code template, or from a .zip.
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createPluginDraft,
  deletePluginDraft,
  importPluginDraft,
  listPluginDrafts,
  type PluginDraft,
} from "./api";
import { fmtStamp } from "./format";
import { errMessage, notifyError } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  cn,
  EmptyState,
  IconButton,
  IconTrash,
  Modal,
  Panel,
  TextInput,
  useConfirm,
} from "./ui";

export function DraftsPanel({ onOpen }: { onOpen: (id: number) => void }) {
  const { t } = useTranslation();
  const { confirm, confirmNode } = useConfirm();
  const [drafts, setDrafts] = useState<PluginDraft[] | null>(null);
  const [creating, setCreating] = useState(false);

  const load = () =>
    listPluginDrafts()
      .then((r) => setDrafts(r.drafts ?? []))
      .catch((e) => {
        setDrafts((cur) => cur ?? []);
        notifyError(errMessage(e));
      });
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per mount (the page remounts it after an edit)
  useEffect(() => {
    load();
  }, []);

  const remove = async (d: PluginDraft) => {
    if (!(await confirm({ title: t("studio.deleteDraft", { name: d.name }), body: t("studio.deleteDraftBody"), danger: true }))) return;
    try {
      await deletePluginDraft(d.id);
      setDrafts((cur) => (cur ?? []).filter((x) => x.id !== d.id));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  return (
    <>
      <Panel
        title={t("studio.draftsTitle")}
        aside={
          <Button size="sm" onClick={() => setCreating(true)}>
            {t("studio.newPlugin")}
          </Button>
        }
      >
        <p className="border-b border-brand-600/10 px-3.5 py-3 text-xs text-ink-muted">{t("studio.draftsIntro")}</p>
        {drafts === null ? (
          <CenterLoader />
        ) : drafts.length === 0 ? (
          <EmptyState title={t("studio.draftsEmpty")} body={t("studio.draftsEmptyHint")} />
        ) : (
          <div className="divide-y divide-brand-600/10">
            {drafts.map((d) => (
              <div key={d.id} className="flex items-center gap-3 px-3.5 py-2.5">
                <button type="button" className="min-w-0 flex-1 text-left" onClick={() => onOpen(d.id)}>
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                    <span className="text-sm font-semibold text-ink">{d.name || d.plugin_id || t("studio.untitled")}</span>
                    {d.plugin_id && (
                      <span className="font-mono text-[11px] text-ink-muted">
                        {d.plugin_id}@{d.version}
                      </span>
                    )}
                    <Badge color={d.mode === "builder" ? "teal" : "gray"} size="xs">
                      {d.mode === "builder" ? t("studio.modeBuilder") : t("studio.modeCode")}
                    </Badge>
                  </div>
                  <p className="mt-0.5 text-[11px] text-ink-muted">
                    {[d.created_by, t("studio.edited", { at: fmtStamp(d.updated_at) })].filter(Boolean).join(" · ")}
                  </p>
                </button>
                <Button size="xs" variant="light" onClick={() => onOpen(d.id)}>
                  {t("studio.open")}
                </Button>
                <IconButton title={t("common.delete")} variant="subtle" color="gray" onClick={() => remove(d)}>
                  <IconTrash size={14} />
                </IconButton>
              </div>
            ))}
          </div>
        )}
      </Panel>
      {creating && (
        <NewPluginDialog
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false);
            onOpen(id);
          }}
        />
      )}
      {confirmNode}
    </>
  );
}

type Start = "builder" | "template" | "zip";

const ID_RE = /^[a-z][a-z0-9-]{2,39}$/;

function NewPluginDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (id: number) => void }) {
  const { t } = useTranslation();
  const [start, setStart] = useState<Start>("builder");
  const [id, setId] = useState("");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const ok = start === "zip" || ID_RE.test(id);

  const create = async (file?: File) => {
    setBusy(true);
    try {
      const v = file ? await importPluginDraft(file) : await createPluginDraft(start === "builder" ? "builder" : "template", id);
      onCreated(v.draft.id);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const choices: { value: Start; title: string; hint: string }[] = [
    { value: "builder", title: t("studio.fromBuilder"), hint: t("studio.fromBuilderHint") },
    { value: "template", title: t("studio.fromCode"), hint: t("studio.fromCodeHint") },
    { value: "zip", title: t("studio.fromZip"), hint: t("studio.fromZipHint") },
  ];

  return (
    <Modal
      open
      onClose={onClose}
      title={t("studio.newTitle")}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          {start === "zip" ? (
            <Button loading={busy} onClick={() => fileRef.current?.click()}>
              {t("studio.chooseZip")}
            </Button>
          ) : (
            <Button loading={busy} disabled={!ok} onClick={() => create()}>
              {t("studio.create")}
            </Button>
          )}
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="grid gap-2" role="radiogroup">
          {choices.map((c) => (
            <button
              key={c.value}
              type="button"
              role="radio"
              aria-checked={start === c.value}
              onClick={() => setStart(c.value)}
              className={cn(
                "rounded-xl border px-4 py-3 text-left transition",
                start === c.value ? "accent-tint border-brand-500" : "accent-tint-hover border-gray-200",
              )}
            >
              <span className="block text-sm font-semibold text-ink">{c.title}</span>
              <span className="mt-0.5 block text-xs text-ink-muted">{c.hint}</span>
            </button>
          ))}
        </div>
        {start !== "zip" && (
          <div>
            <TextInput
              label={t("studio.pluginId")}
              value={id}
              onChange={(v) => setId(v.toLowerCase().replace(/[^a-z0-9-]/g, ""))}
              placeholder="welcome-bot"
              mono
              autoFocus
            />
            <p className="mt-1 text-[11px] text-ink-muted">{t("studio.pluginIdHint")}</p>
          </div>
        )}
        <input
          ref={fileRef}
          type="file"
          accept=".zip,application/zip"
          className="hidden"
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) create(f);
          }}
        />
      </div>
    </Modal>
  );
}
