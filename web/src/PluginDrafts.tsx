// Drafts: plugins written in the panel (see PluginStudio), in the one plugins list.
// CreatePlugin is the "make your own" half of the Add dialog; DraftRow is a draft
// not installed yet, among the installed plugins.
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { createPluginDraft, importPluginDraft, type PluginDraft } from "./api";
import { fmtStamp } from "./format";
import { errMessage, notifyError } from "./notify";
import { Button, cn, HintDot, IconButton, IconPencil, IconTrash, Spinner, TextInput } from "./ui";

type Start = "builder" | "template";

const ID_RE = /^[a-z][a-z0-9-]{2,39}$/;

// CreatePlugin starts a draft — from the rule builder, the code template, or the
// sources the editor's Download gave — and hands its id on for the editor to open.
export function CreatePlugin({ onCreated }: { onCreated: (draftId: number) => void }) {
  const { t } = useTranslation();
  const [start, setStart] = useState<Start | null>(null);
  const [id, setId] = useState("");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);

  // The sources zip (with test.js, and rules.json for a builder draft) opens as a
  // draft; a package to install goes to the drop zone above instead.
  const importSources = async (f: File | undefined) => {
    if (!f) return;
    setImporting(true);
    try {
      const v = await importPluginDraft(f);
      onCreated(v.draft.id);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setImporting(false);
    }
  };

  const create = async () => {
    if (!start) return;
    setBusy(true);
    try {
      const v = await createPluginDraft(start, id);
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
  ];

  return (
    <div className="flex flex-col gap-3">
      <input
        ref={fileRef}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          importSources(e.currentTarget.files?.[0]);
          e.currentTarget.value = "";
        }}
      />
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup">
        {choices.map((c) => (
          <button
            key={c.value}
            type="button"
            role="radio"
            aria-checked={start === c.value}
            onClick={() => setStart(c.value)}
            className={cn(
              "flex flex-col justify-start rounded-xl border px-4 py-3 text-left transition",
              start === c.value ? "accent-tint border-brand-500" : "accent-tint-hover border-gray-200",
            )}
          >
            <span className="block text-sm font-semibold text-ink">{c.title}</span>
            <span className="mt-0.5 block text-xs text-ink-muted">{c.hint}</span>
          </button>
        ))}
        <button
          type="button"
          disabled={importing}
          onClick={() => {
            setStart(null);
            fileRef.current?.click();
          }}
          className="accent-tint-hover flex flex-col justify-start rounded-xl border border-gray-200 px-4 py-3 text-left transition disabled:opacity-60"
        >
          <span className="flex items-center gap-1.5 text-sm font-semibold text-ink">
            {t("studio.fromSources")}
            {importing && <Spinner size={12} />}
          </span>
          <span className="mt-0.5 block text-xs text-ink-muted">{t("studio.fromSourcesHint")}</span>
        </button>
      </div>
      {start && (
        <form
          className="flex flex-col gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            if (ID_RE.test(id)) create();
          }}
        >
          <div className="flex items-end gap-2">
            <div className="min-w-0 flex-1">
              <TextInput
                label={t("studio.pluginId")}
                value={id}
                onChange={(v) => setId(v.toLowerCase().replace(/[^a-z0-9-]/g, ""))}
                placeholder="welcome-bot"
                mono
                autoFocus
              />
            </div>
            <Button type="submit" loading={busy} disabled={!ID_RE.test(id)}>
              {t("studio.create")}
            </Button>
          </div>
          <p className="text-[11px] text-ink-muted">{t("studio.pluginIdHint")}</p>
        </form>
      )}
    </div>
  );
}

// DraftRow is a plugin made in the panel and not installed yet: the editor, the
// install, or away with it.
export function DraftRow({
  draft: d,
  onOpen,
  onInstall,
  onRemove,
}: {
  draft: PluginDraft;
  onOpen: () => void;
  onInstall: () => Promise<void>;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  return (
    <div className="px-3.5 py-2.5">
      <div className="flex items-center gap-3">
        <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1">
          <button type="button" className="break-words text-left text-sm font-semibold text-ink" onClick={onOpen}>
            {d.name || d.plugin_id || t("studio.untitled")}
          </button>
          {d.version && <span className="text-xs text-ink-muted">{t("plugins.version", { v: d.version })}</span>}
          <HintDot color="gray" label={t("studio.draftNote", { at: fmtStamp(d.updated_at) })}>
            {t("studio.draftNote", { at: fmtStamp(d.updated_at) })}
          </HintDot>
        </div>
        <div className="-my-1 flex shrink-0 items-center gap-0.5">
          <IconButton title={t("studio.openInEditor")} variant="subtle" color="gray" onClick={onOpen}>
            <IconPencil size={16} />
          </IconButton>
          <IconButton title={t("studio.deleteDraftShort")} variant="subtle" color="red" onClick={onRemove}>
            <IconTrash size={16} />
          </IconButton>
          <span className="ml-2">
            <Button
              size="xs"
              variant="light"
              loading={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  await onInstall();
                } finally {
                  setBusy(false);
                }
              }}
            >
              {t("studio.install")}
            </Button>
          </span>
        </div>
      </div>
    </div>
  );
}
