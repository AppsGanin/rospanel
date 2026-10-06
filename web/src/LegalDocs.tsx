import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getLegal, type LegalKind, type LegalView, previewLegal, saveLegal } from "./api";
import { fmtStamp } from "./format";
import { errMessage } from "./notify";
import { Panel, SegmentedControl, SettingRow, Textarea } from "./ui";

// The operator's user agreement and privacy policy, in Markdown: shown on the
// subscription page, in the user bot and over the API. The page that edits the
// branding saves them with its own Save, so the state lives here as a hook and the
// panel it renders goes where that page puts it.

const KINDS: LegalKind[] = ["terms", "privacy"];

const EMPTY: Record<LegalKind, string> = { terms: "", privacy: "" };

export function useLegalDocs() {
  const [saved, setSaved] = useState<Record<LegalKind, LegalView> | null>(null);
  const [draft, setDraft] = useState<Record<LegalKind, string>>(EMPTY);

  useEffect(() => {
    getLegal()
      .then((v) => {
        setSaved(v);
        setDraft({ terms: v.terms.markdown, privacy: v.privacy.markdown });
      })
      .catch(() => {});
  }, []);

  const dirty = !!saved && KINDS.some((k) => draft[k] !== saved[k].markdown);

  const save = async () => {
    if (!saved || !dirty) return;
    const changed: Partial<Record<LegalKind, string>> = {};
    for (const k of KINDS) if (draft[k] !== saved[k].markdown) changed[k] = draft[k];
    const v = await saveLegal(changed);
    setSaved(v);
    setDraft({ terms: v.terms.markdown, privacy: v.privacy.markdown });
  };

  const cancel = () => {
    if (saved) setDraft({ terms: saved.terms.markdown, privacy: saved.privacy.markdown });
  };

  const panel = saved ? (
    <LegalDocsPanel saved={saved} draft={draft} onChange={(k, v) => setDraft((d) => ({ ...d, [k]: v }))} />
  ) : null;

  return { panel, dirty, save, cancel };
}

function LegalDocsPanel({
  saved,
  draft,
  onChange,
}: {
  saved: Record<LegalKind, LegalView>;
  draft: Record<LegalKind, string>;
  onChange: (k: LegalKind, v: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Panel title={t("legal.title")}>
      <SettingRow hint={t("legal.hint")} />
      {KINDS.map((k) => (
        <LegalDocEditor key={k} kind={k} saved={saved[k]} value={draft[k]} onChange={(v) => onChange(k, v)} />
      ))}
    </Panel>
  );
}

function LegalDocEditor({
  kind,
  saved,
  value,
  onChange,
}: {
  kind: LegalKind;
  saved: LegalView;
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const [html, setHtml] = useState("");
  const [err, setErr] = useState("");

  // The preview is the server's rendering — the same one the page and the bot link
  // to — of the text as it stands, saved or not.
  useEffect(() => {
    if (mode !== "preview") return;
    let live = true;
    previewLegal(value)
      .then((r) => live && (setHtml(r.html), setErr("")))
      .catch((e) => live && setErr(errMessage(e)));
    return () => {
      live = false;
    };
  }, [mode, value]);

  const meta = [
    saved.updated_at ? t("legal.updated", { when: fmtStamp(saved.updated_at) }) : "",
  ].filter(Boolean);

  return (
    <SettingRow>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-xs font-semibold text-ink">{t(`legal.${kind}`)}</span>
          {saved.url && (
            <a
              href={saved.url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-[11px] text-accent underline-offset-2 hover:underline"
            >
              {t("legal.open")}
            </a>
          )}
          {meta.length > 0 && <span className="text-[11px] text-ink-muted">{meta.join(" · ")}</span>}
          <span className="ml-auto">
            <SegmentedControl
              size="xs"
              nav
              value={mode}
              onChange={(v) => setMode(v as "edit" | "preview")}
              data={[
                { value: "edit", label: t("legal.edit") },
                { value: "preview", label: t("legal.preview") },
              ]}
            />
          </span>
        </div>
        {mode === "edit" ? (
          <Textarea rows={10} value={value} onChange={onChange} placeholder={t(kind === "terms" ? "legal.placeholderTerms" : "legal.placeholderPrivacy")} />
        ) : err ? (
          <p className="text-xs text-danger">{err}</p>
        ) : value.trim() === "" ? (
          <p className="text-xs text-ink-muted">{t("legal.empty")}</p>
        ) : (
          <div
            className="legal-preview max-h-[50vh] overflow-y-auto rounded-lg border border-gray-200 px-4 py-3 text-sm text-ink"
            // Safe to inject: the server renders the Markdown and drops raw HTML from it.
            dangerouslySetInnerHTML={{ __html: html }}
          />
        )}
      </div>
    </SettingRow>
  );
}
