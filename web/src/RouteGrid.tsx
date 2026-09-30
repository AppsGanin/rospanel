import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ApiRoute } from "./api";
import { td } from "./i18n";
import { cn, IconCheck, TextInput } from "./ui";

// RouteGrid is where an API key is given its methods: every /v1 call it may make,
// in the published spec's sections. A section folds away with a box of its own that
// ticks or clears the whole of it; a method the admin may not call themselves shows
// greyed, and the server refuses it anyway.

const METHOD_TONE: Record<string, string> = {
  GET: "text-success",
  POST: "text-brand-600",
  PATCH: "text-warning",
  DELETE: "text-danger",
};

export function RouteGrid({
  routes,
  selected,
  onChange,
}: {
  routes: ApiRoute[];
  selected: Set<string>;
  onChange: (next: Set<string>) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  // Sections opened by hand; a search opens every section it finds something in.
  const [open, setOpen] = useState<Set<string>>(new Set());

  const label = (r: ApiRoute) => td(`apiRoute.${r.key}`);
  const q = query.trim().toLowerCase();
  const shown = useMemo(
    () =>
      q
        ? routes.filter(
            (r) =>
              td(`apiRoute.${r.key}`).toLowerCase().includes(q) ||
              r.route.toLowerCase().includes(q),
          )
        : routes,
    [routes, q],
  );
  const sections = useMemo(() => {
    const out: { tag: string; items: ApiRoute[] }[] = [];
    for (const r of shown) {
      const s = out.find((x) => x.tag === r.tag);
      if (s) s.items.push(r);
      else out.push({ tag: r.tag, items: [r] });
    }
    return out;
  }, [shown]);

  const set = (items: ApiRoute[], on: boolean) => {
    const next = new Set(selected);
    for (const r of items) {
      if (!r.grantable) continue;
      if (on) next.add(r.route);
      else next.delete(r.route);
    }
    onChange(next);
  };
  const grantable = routes.filter((r) => r.grantable);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="min-w-0 flex-1 basis-48">
          <TextInput value={query} onChange={setQuery} placeholder={t("api.routesSearch")} />
        </div>
        <span className="flex gap-3 text-xs">
          <button
            type="button"
            className="text-brand-600 hover:underline"
            onClick={() => onChange(new Set(grantable.map((r) => r.route)))}
          >
            {t("api.routesAll")}
          </button>
          <button
            type="button"
            className="text-brand-600 hover:underline"
            onClick={() =>
              onChange(new Set(grantable.filter((r) => r.method === "GET").map((r) => r.route)))
            }
          >
            {t("api.routesRead")}
          </button>
          <button
            type="button"
            className="text-ink-muted hover:underline"
            onClick={() => onChange(new Set())}
          >
            {t("api.routesNone")}
          </button>
        </span>
      </div>
      <div className="overflow-hidden rounded-xl border border-gray-200">
        {sections.length === 0 && (
          <p className="px-3.5 py-3 text-xs text-ink-muted">{t("api.routesEmpty")}</p>
        )}
        {sections.map((s, i) => {
          const can = s.items.filter((r) => r.grantable);
          const on = s.items.filter((r) => selected.has(r.route)).length;
          const expanded = !!q || open.has(s.tag);
          return (
            <div key={s.tag} className={cn(i > 0 && "border-t border-gray-100")}>
              <div className="flex items-center gap-2.5 bg-gray-50 px-3.5 py-2">
                <Box
                  checked={can.length > 0 && can.every((r) => selected.has(r.route))}
                  partial={on > 0}
                  disabled={can.length === 0}
                  label={td(`apiTag.${s.tag}`)}
                  onChange={(v) => set(s.items, v)}
                />
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  onClick={() =>
                    setOpen((cur) => {
                      const next = new Set(cur);
                      if (next.has(s.tag)) next.delete(s.tag);
                      else next.add(s.tag);
                      return next;
                    })
                  }
                  aria-expanded={expanded}
                >
                  <span className="truncate text-xs font-medium text-ink">
                    {td(`apiTag.${s.tag}`)}
                  </span>
                  <span className="shrink-0 font-mono text-[11px] text-ink-muted">
                    {on}/{s.items.length}
                  </span>
                  <span
                    className={cn(
                      "ml-auto shrink-0 text-ink-muted transition",
                      expanded && "rotate-90",
                    )}
                    aria-hidden
                  >
                    ›
                  </span>
                </button>
              </div>
              {expanded &&
                s.items.map((r) => (
                  <div
                    key={r.route}
                    className={cn(
                      "flex items-center gap-2.5 border-t border-gray-100 py-[6px] pr-3.5 pl-7",
                      !r.grantable && "opacity-50",
                    )}
                  >
                    <Box
                      checked={selected.has(r.route)}
                      disabled={!r.grantable && !selected.has(r.route)}
                      label={label(r)}
                      onChange={(v) => set([r], v)}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs text-ink">{label(r)}</span>
                      <span className="block truncate font-mono text-[10.5px] text-ink-muted">
                        <span className={cn("font-semibold", METHOD_TONE[r.method])}>
                          {r.method}
                        </span>{" "}
                        {r.path}
                      </span>
                    </span>
                  </div>
                ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Box is one compact check: ticked, part-ticked (a section with some of its methods),
// or empty.
function Box({
  checked,
  partial,
  disabled,
  label,
  onChange,
}: {
  checked: boolean;
  partial?: boolean;
  disabled?: boolean;
  label: string;
  onChange: (v: boolean) => void;
}) {
  const filled = checked || partial;
  return (
    <label
      className={cn(
        "relative flex shrink-0 items-center p-0.5",
        disabled ? "cursor-not-allowed opacity-40" : "cursor-pointer",
      )}
      title={label}
    >
      <input
        type="checkbox"
        className="sr-only"
        checked={checked}
        disabled={disabled}
        aria-label={label}
        aria-checked={checked ? "true" : partial ? "mixed" : "false"}
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
      <span
        className={cn(
          "flex size-4 items-center justify-center rounded-sm border transition",
          filled
            ? "border-brand-600 bg-brand-600 text-onbrand"
            : "border-gray-300 bg-white hover:border-gray-400",
        )}
      >
        {checked ? (
          <IconCheck size={12} />
        ) : partial ? (
          <span className="h-0.5 w-2 rounded-full bg-current" />
        ) : null}
      </span>
    </label>
  );
}
