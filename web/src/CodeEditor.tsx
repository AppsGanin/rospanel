// The plugin editor's code view: CodeMirror 6, loaded only when a draft is opened
// (PluginStudio imports it lazily) so the panel's own bundle does not carry it.
//
// Colours come from the panel's CSS variables, so the editor follows the light and
// dark themes and the configured accent like the rest of the page.
import { autocompletion, type CompletionContext, completeFromList } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { sql } from "@codemirror/lang-sql";
import {
  bracketMatching,
  HighlightStyle,
  indentOnInput,
  syntaxHighlighting,
} from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { useEffect, useRef } from "react";
import type { CodeLang } from "./codeLang";


const languages: Record<CodeLang, () => Extension> = {
  js: () => [javascript(), autocompletion({ override: [panelCompletions, jsKeywords] })],
  json: () => json(),
  sql: () => sql(),
  md: () => markdown(),
  text: () => [],
};

const theme = EditorView.theme({
  "&": {
    height: "100%",
    fontSize: "13px",
    backgroundColor: "var(--color-white)",
    color: "var(--color-ink)",
  },
  ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.55" },
  ".cm-content": { caretColor: "var(--color-ink)", padding: "8px 0" },
  ".cm-cursor": { borderLeftColor: "var(--color-ink)" },
  ".cm-gutters": {
    backgroundColor: "transparent",
    color: "var(--color-ink-muted)",
    border: "none",
    paddingRight: "4px",
  },
  ".cm-activeLine, .cm-activeLineGutter": {
    backgroundColor: "color-mix(in srgb, var(--color-brand-600) 6%, transparent)",
  },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in srgb, var(--color-brand-600) 22%, transparent) !important",
  },
  ".cm-matchingBracket": {
    backgroundColor: "color-mix(in srgb, var(--color-brand-600) 18%, transparent)",
    outline: "none",
  },
  ".cm-tooltip": {
    backgroundColor: "var(--color-white)",
    color: "var(--color-ink)",
    border: "1px solid color-mix(in srgb, var(--color-ink) 15%, transparent)",
    borderRadius: "8px",
    overflow: "hidden",
  },
  ".cm-tooltip-autocomplete ul li[aria-selected]": {
    backgroundColor: "color-mix(in srgb, var(--color-brand-600) 18%, transparent)",
    color: "var(--color-ink)",
  },
  ".cm-completionDetail": { color: "var(--color-ink-muted)", fontStyle: "normal", marginLeft: "8px" },
  ".cm-panels": { backgroundColor: "var(--color-white)", color: "var(--color-ink)" },
  "&.cm-focused": { outline: "none" },
});

const highlight = HighlightStyle.define([
  { tag: [tags.keyword, tags.controlKeyword, tags.moduleKeyword, tags.operatorKeyword], color: "var(--accent-fg, #0a3fb0)", fontWeight: "600" },
  { tag: [tags.string, tags.special(tags.string)], color: "var(--success-fg, #047857)" },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], color: "var(--warning-fg, #c2410c)" },
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: "var(--color-ink-muted)", fontStyle: "italic" },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: "var(--accent-fg, #0a3fb0)" },
  { tag: [tags.propertyName, tags.attributeName], color: "var(--color-ink)" },
  { tag: [tags.heading], fontWeight: "700" },
  { tag: [tags.invalid], color: "var(--danger-fg, #b91c1c)" },
]);

// What a plugin reaches through `panel.` — the same as rospanel.d.ts, as the
// completion list shows it.
const PANEL: { label: string; detail: string; info?: string }[] = [
  { label: "panel.api", detail: "(method, path, body?) → {status, body}", info: "The panel's /v1 API with the plugin's permissions." },
  { label: "panel.users.get", detail: "(id) → {status, body}" },
  { label: "panel.users.list", detail: "(query?) → {status, body}" },
  { label: "panel.users.update", detail: "(id, patch) → {status, body}" },
  { label: "panel.config", detail: "settings, all strings", info: "The plugin's settings: what the operator filled in." },
  { label: "panel.kv.get", detail: "(key) → value | null" },
  { label: "panel.kv.set", detail: "(key, value)" },
  { label: "panel.kv.delete", detail: "(key)" },
  { label: "panel.kv.list", detail: "(prefix, {limit?, after?}) → [{key, value}]" },
  { label: "panel.db.query", detail: "(sql, ...args) → rows" },
  { label: "panel.db.exec", detail: "(sql, ...args) → {changes, last_insert_id}" },
  { label: "panel.http.fetch", detail: "(url, {method, headers, body}) → {status, headers, body}", info: "Only to the hosts in plugin.json's net." },
  { label: "panel.log.info", detail: "(msg, fields?)" },
  { label: "panel.log.warn", detail: "(msg, fields?)" },
  { label: "panel.log.error", detail: "(msg, fields?)" },
  { label: "panel.t", detail: "(key, params?, lang?) → string", info: "A string from i18n/<lang>.json." },
  { label: "panel.crypto.hash", detail: "(alg, data, enc?) → string" },
  { label: "panel.crypto.hmac", detail: "(alg, key, data, enc?) → string" },
  { label: "panel.crypto.sign", detail: "(alg, privateKeyPem, data) → string" },
  { label: "panel.crypto.verify", detail: "(alg, keyPem, data, signatureBase64) → boolean" },
  { label: "panel.crypto.jwt", detail: "(alg, privateKeyPem, claims, header?) → string" },
  { label: "panel.crypto.randomHex", detail: "(bytes?) → string" },
  { label: "panel.crypto.randomUUID", detail: "() → string" },
  { label: "panel.blob.from", detail: "(data) → Blob" },
  { label: "panel.blob.text", detail: "(blob, enc?) → string" },
];

function panelCompletions(ctx: CompletionContext) {
  const word = ctx.matchBefore(/panel(\.[\w]*)*\.?/);
  if (!word || (word.from === word.to && !ctx.explicit)) return null;
  return {
    from: word.from,
    options: PANEL.map((p) => ({
      label: p.label,
      detail: p.detail,
      info: p.info,
      type: p.detail.startsWith("(") ? "function" : "property",
    })),
    validFor: /^panel(\.[\w]*)*$/,
  };
}

const jsKeywords = completeFromList(
  ["export function", "const", "let", "return", "if", "else", "for", "of", "throw new Error", "try", "catch", "JSON.stringify", "JSON.parse"].map(
    (label) => ({ label, type: "keyword" }),
  ),
);

export default function CodeEditor({
  value,
  onChange,
  lang,
  readOnly = false,
}: {
  value: string;
  onChange?: (v: string) => void;
  lang: CodeLang;
  readOnly?: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const langSlot = useRef(new Compartment());
  const roSlot = useRef(new Compartment());
  const change = useRef(onChange);
  change.current = onChange;

  // biome-ignore lint/correctness/useExhaustiveDependencies: one view per mount; value/lang/readOnly sync below
  useEffect(() => {
    if (!host.current) return;
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          drawSelection(),
          indentOnInput(),
          bracketMatching(),
          highlightSelectionMatches(),
          EditorState.tabSize.of(2),
          keymap.of([indentWithTab, ...defaultKeymap, ...historyKeymap, ...searchKeymap]),
          theme,
          syntaxHighlighting(highlight),
          langSlot.current.of(languages[lang]()),
          roSlot.current.of([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) change.current?.(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => {
      v.destroy();
      view.current = null;
    };
  }, []);

  // A different file, or the server's copy after a save: replace the text unless it
  // is what the editor already holds (typing must not lose the cursor).
  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
    }
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({ effects: langSlot.current.reconfigure(languages[lang]()) });
  }, [lang]);

  useEffect(() => {
    view.current?.dispatch({
      effects: roSlot.current.reconfigure([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
    });
  }, [readOnly]);

  return <div ref={host} className="h-full min-h-0 overflow-hidden" />;
}
