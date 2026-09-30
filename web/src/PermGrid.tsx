import { useTranslation } from "react-i18next";
import type { Perm, PermSection } from "./api";
import { td } from "./i18n";
import { cn, IconCheck, MICRO } from "./ui";

// PermGrid is the permission table a role and an API key are both ticked in:
// section, then a "view" and a "change" column. The rows come from the server's
// catalog, so a permission added there reaches every editor without a second list.

export const permLabel = (key: string) => td(`permSection.${key}`);

// A permission of its own sits in the column it reads as — the journal and the logs
// are things you look at, a backup or an update is something you do.
const GRID = "minmax(0,1fr) 72px 72px";

// closure is a permission with everything it brings (manage → view, …), the same
// expansion the server stores.
export function permClosure(
  implies: Partial<Record<Perm, Perm[]>>,
  p: Perm,
  into = new Set<Perm>(),
): Set<Perm> {
  if (into.has(p)) return into;
  into.add(p);
  for (const q of implies[p] ?? []) permClosure(implies, q, into);
  return into;
}

export function PermGrid({
  catalog,
  implies,
  perms,
  onChange,
  allowed,
}: {
  catalog: PermSection[];
  implies: Partial<Record<Perm, Perm[]>>;
  perms: Set<Perm>;
  onChange: (next: Set<Perm>) => void;
  // What may be ticked; the rest shows greyed. Every permission when left out.
  allowed?: Set<Perm>;
}) {
  const { t } = useTranslation();
  // A box can be ticked only if everything it brings may be: ticking "change" on a
  // section whose "view" is out of reach would store a set nobody may grant.
  const can = (p: Perm) =>
    !allowed || [...permClosure(implies, p)].every((q) => allowed.has(q));

  // Ticking a box ticks what it brings; unticking one unticks everything that would
  // bring it back — so the grid never shows a set the server would not store.
  const toggle = (p: Perm, on: boolean) => {
    const next = new Set(perms);
    if (on) {
      for (const q of permClosure(implies, p)) next.add(q);
    } else {
      for (const q of [...next]) if (permClosure(implies, q).has(p)) next.delete(q);
    }
    onChange(next);
  };

  const cell = (s: PermSection, which: "view" | "manage") => {
    const p = s[which];
    if (!p) return null;
    const col = which === "view" ? t("rolesPanel.colView") : t("rolesPanel.colManage");
    return (
      <PermCheck
        checked={perms.has(p)}
        disabled={!can(p) && !perms.has(p)}
        onChange={(v) => toggle(p, v)}
        label={`${permLabel(s.key)}: ${col}`}
      />
    );
  };

  return (
    <div className="overflow-hidden rounded-xl border border-gray-200">
      <div
        className={cn(MICRO, "grid items-center gap-3 bg-gray-50 px-3.5 py-2")}
        style={{ gridTemplateColumns: GRID }}
      >
        <span>{t("rolesPanel.colSection")}</span>
        <span className="text-center">{t("rolesPanel.colView")}</span>
        <span className="text-center">{t("rolesPanel.colManage")}</span>
      </div>
      {catalog.map((s) => (
        <div
          key={s.key}
          className="grid items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
          style={{ gridTemplateColumns: GRID }}
        >
          <span className="min-w-0 text-xs text-ink">{permLabel(s.key)}</span>
          <span className="flex justify-center">{cell(s, "view")}</span>
          <span className="flex justify-center">{cell(s, "manage")}</span>
        </div>
      ))}
    </div>
  );
}

// PermCheck is one box of the grid: the list's own compact check, not the card-style
// Checkbox, which is built for a single choice with a sentence beside it.
function PermCheck({
  checked,
  disabled,
  onChange,
  label,
}: {
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
  label: string;
}) {
  return (
    <label
      className={cn(
        "relative flex items-center p-1",
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
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
      <span
        className={cn(
          "flex size-4 items-center justify-center rounded-sm border transition",
          checked
            ? "border-brand-600 bg-brand-600 text-onbrand"
            : "border-gray-300 bg-white hover:border-gray-400",
        )}
      >
        {checked && <IconCheck size={12} />}
      </span>
    </label>
  );
}
