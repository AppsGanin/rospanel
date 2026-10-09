// What a rule can read: the fields of its event, described, with examples, and the
// event itself as a sample. A click puts a field where the cursor was — a
// placeholder into a text, a path into a condition — or, with no such field, on the
// clipboard.
import { type FocusEvent, type PointerEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { EventCatalog, EventField, EventInfo } from "./api";
import { fmtBytes, fmtStamp } from "./format";
import { td } from "./i18n";
import { notifySuccess } from "./notify";
import { Code, cn, IconChevron } from "./ui";

// Formats a placeholder can show a field through, by the field's type (runtime.js's
// FORMATS; builder.Formats checks them).
const FORMATS: Partial<Record<EventField["type"], Format[]>> = {
  time: ["date", "datetime", "days"],
  bytes: ["gb"],
  kop: ["rub"],
};
type Format = "date" | "datetime" | "days" | "gb" | "rub";

// fieldsFor is every field a rule on the event (or on a schedule) can read.
export function fieldsFor(catalog: EventCatalog, info: EventInfo | undefined, scheduled: boolean): EventField[] {
  if (scheduled) return catalog.context.filter((f) => f.path === "now" || f.path === "created_at");
  const user = info && (info.user === "data" || info.user === "nested") ? catalog.user : [];
  return [...user, ...(info?.fields ?? []), ...catalog.context];
}

// eventSample is the event as the plugin gets it.
export function eventSample(info: EventInfo) {
  return { id: "9f2c4e1a7b3d5f60", event: info.event, created_at: 1767225600, data: info.sample };
}

// useFieldTarget remembers the last text a field can go into: one marked
// data-fill (a placeholder goes in at the cursor) or data-path (a condition's field,
// replaced). Put onFocus and onPointerDown on the box that holds them.
export function useFieldTarget() {
  const { t } = useTranslation();
  const target = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);

  const onFocus = (e: FocusEvent<HTMLElement>) => {
    const el = e.target;
    if (el.closest("[data-fields]")) return; // a field picked by the keyboard keeps the place
    if (!(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement)) return;
    // Any other field (a condition's value, the rule's name) takes the cursor away:
    // a pick then goes to the clipboard, not into a text edited before.
    target.current =
      !(el instanceof HTMLSelectElement) && (el.dataset.path !== undefined || el.closest("[data-fill]")) ? el : null;
  };

  // put inserts the field — through a format, when given — where the cursor was.
  // A press on anything else of the rule — the operator's list, a switch, a button
  // — moves away from the text too; the field list itself keeps it (it is where the
  // pick is made). A focus does not come from every such press (Safari does not
  // focus a clicked button), so the press is read.
  const onPointerDown = (e: PointerEvent<HTMLElement>) => {
    const el = e.target as HTMLElement;
    if (el.closest("[data-fields]")) return;
    const field = el.closest("input, textarea");
    if (!field || !(field.matches("[data-path]") || field.closest("[data-fill]"))) target.current = null;
  };

  const put = (path: string, format?: string) => {
    const el = target.current;
    const placeholder = `{{${path}${format ? `|${format}` : ""}}}`;
    if (!el?.isConnected) {
      navigator.clipboard?.writeText(placeholder).then(
        () => notifySuccess(t("studio.dataCopied", { path: placeholder })),
        () => {},
      );
      return;
    }
    // A condition reads the value as it is: a format has no place there.
    const whole = el.dataset.path !== undefined;
    const ins = whole ? path : placeholder;
    const start = whole ? 0 : (el.selectionStart ?? el.value.length);
    const end = whole ? el.value.length : (el.selectionEnd ?? el.value.length);
    const next = el.value.slice(0, start) + ins + el.value.slice(end);
    // React owns the value: set it the way typing does, so its onChange sees it.
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")?.set?.call(el, next);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.focus();
    const caret = start + ins.length;
    requestAnimationFrame(() => el.setSelectionRange(caret, caret));
  };

  return { onFocus, onPointerDown, put };
}

// example shows a field's sample value, read the way a person reads it.
function example(f: EventField): string {
  const v = f.example;
  if (v === "" || v === null || v === undefined) return "";
  const raw = typeof v === "object" ? JSON.stringify(v) : String(v);
  if (typeof v !== "number" || v === 0) return raw;
  switch (f.type) {
    case "time":
      return `${raw} · ${fmtStamp(v)}`;
    case "bytes":
      return `${raw} · ${fmtBytes(v)}`;
    case "kop":
      return `${raw} · ${(v / 100).toLocaleString()} ₽`;
  }
  return raw;
}

export function EventDataPanel({
  catalog,
  info,
  scheduled,
  onPick,
}: {
  catalog: EventCatalog;
  info: EventInfo | undefined;
  scheduled: boolean;
  onPick: (path: string, format?: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [json, setJson] = useState(false);
  const all = fieldsFor(catalog, info, scheduled);
  const hasUser = !!info && (info.user === "data" || info.user === "nested");
  const groups: { title: string; fields: EventField[] }[] = scheduled
    ? [{ title: t("studio.groupCommon"), fields: all }]
    : [
        { title: t("studio.groupUser"), fields: hasUser ? catalog.user : [] },
        { title: t("studio.groupEvent"), fields: info?.fields ?? [] },
        { title: t("studio.groupCommon"), fields: catalog.context },
      ].filter((g) => g.fields.length > 0);

  return (
    <div data-fields className="rounded-xl border border-gray-200">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-ink"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <IconChevron size={12} className={cn("text-ink-muted transition", open ? "" : "-rotate-90")} />
        {t("studio.dataTitle")}
        <span className="font-normal text-ink-muted">· {all.length}</span>
      </button>
      {open && (
        <div className="flex flex-col gap-3 border-t border-gray-100 px-3 py-3">
          <p className="text-[11px] text-ink-muted">
            {scheduled
              ? t("studio.scheduleData")
              : info?.user === ""
                ? t("studio.noUser")
                : info && !info.acts
                  ? t("studio.userGone")
                  : info?.user === "id"
                    ? t("studio.userFromId")
                    : t("studio.dataHint")}
          </p>
          {groups.map((g) => (
            <div key={g.title} className="flex flex-col">
              <p className="mb-1 text-[10px] font-bold tracking-wide text-ink-muted uppercase">{g.title}</p>
              {g.fields.map((f) => (
                <div key={f.path} className="accent-tint-hover -mx-1.5 rounded-md px-1.5 py-1">
                  <button
                    type="button"
                    onMouseDown={(e) => e.preventDefault() /* keep the cursor where it was */}
                    onClick={() => onPick(f.path)}
                    className="grid w-full grid-cols-1 gap-x-3 text-left sm:grid-cols-[11rem_1fr]"
                  >
                    <code className="truncate font-mono text-[12px] text-accent">
                      {f.path}
                      {f.maybe && (
                        // "?" as TypeScript writes an optional field: not on every such event.
                        <span title={t("studio.maybeField")} className="text-ink-muted">
                          ?
                        </span>
                      )}
                    </code>
                    <span className="min-w-0 text-[12px] text-ink">
                      {td(`evField.${f.hint}`)}
                      {example(f) && (
                        <span className="ml-1.5 font-mono text-[11px] break-words text-ink-muted">{example(f)}</span>
                      )}
                    </span>
                  </button>
                  {FORMATS[f.type] && (
                    <div className="mt-0.5 flex flex-wrap gap-1 sm:pl-[11.75rem]">
                      {FORMATS[f.type]?.map((fm) => (
                        <button
                          key={fm}
                          type="button"
                          title={t(`studio.fmt.${fm}`)}
                          onMouseDown={(e) => e.preventDefault()}
                          onClick={() => onPick(f.path, fm)}
                          className="rounded border border-gray-200 px-1.5 font-mono text-[11px] text-ink-muted transition hover:border-brand-500 hover:text-accent"
                        >
                          |{fm}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ))}
          {info && !scheduled && (
            <div>
              <button
                type="button"
                className="flex items-center gap-1.5 text-[11px] font-semibold text-ink-muted hover:text-ink"
                aria-expanded={json}
                onClick={() => setJson(!json)}
              >
                <IconChevron size={10} className={cn("transition", json ? "" : "-rotate-90")} />
                {t("studio.sampleJSON")}
              </button>
              {json && (
                <div className="mt-1.5 flex flex-col gap-1">
                  <p className="text-[11px] text-ink-muted">{t("studio.sampleHint")}</p>
                  <Code block copy>
                    {JSON.stringify(eventSample(info), null, 2)}
                  </Code>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
