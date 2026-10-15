// The language picker. Each name is in its own language (and tagged so), so a
// reader finds theirs whatever the panel shows now. Three shapes of the same control: a
// row of pills for the screens that have no account menu (login, the first-run wizard,
// the forced password change), a globe in the desktop's top bar, and a folding row in
// the phone's account sheet.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { currentLang, LANGS, setLang, type Lang } from "./i18n";
import { cn, Dropdown, DropdownItem, IconCheck, IconChevron, IconGlobe } from "./ui";

// LangPills sits in a corner of the unauthenticated screens — those have no menu
// to hide it in, and an admin who cannot read the login form cannot get far enough
// to find a setting.
export function LangPills({ className }: { className?: string }) {
  const { t } = useTranslation();
  const active = currentLang();

  return (
    <div
      className={cn("flex flex-wrap items-center gap-1", className)}
      role="group"
      aria-label={t("common.language")}
    >
      {LANGS.map((l) => (
        <button
          key={l.code}
          type="button"
          lang={l.code}
          onClick={() => setLang(l.code as Lang)}
          aria-pressed={active === l.code}
          className={cn(
            "whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium transition",
            active === l.code
              ? "bg-brand-600 text-onbrand"
              : "text-ink-muted hover:bg-gray-100",
          )}
        >
          {l.label}
        </button>
      ))}
    </div>
  );
}

// LangRow is the picker as a row of the phone's account sheet: the language now, and the
// list folded under it — a row like its neighbours rather than five pills that do not
// fit beside a label.
export function LangRow() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const active = currentLang();
  const current = LANGS.find((l) => l.code === active)?.label ?? active;

  return (
    <div className="border-t border-gray-100">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3.5 py-2.5 text-start text-[13px] font-medium text-ink"
      >
        <span className="flex-1">{t("common.language")}</span>
        <span lang={active} className="text-ink-muted">
          {current}
        </span>
        <IconChevron
          size={14}
          className={cn("shrink-0 text-ink-muted transition", !open && "-rotate-90 rtl:rotate-90")}
        />
      </button>
      {open && (
        <div className="pb-1.5" role="group" aria-label={t("common.language")}>
          {LANGS.map((l) => (
            <button
              key={l.code}
              type="button"
              lang={l.code}
              onClick={() => {
                setOpen(false);
                void setLang(l.code as Lang);
              }}
              aria-pressed={active === l.code}
              className="flex w-full items-center justify-between gap-2 py-2 ps-7 pe-3.5 text-start text-[13px] transition active:bg-gray-50"
            >
              <span className={active === l.code ? "font-medium text-accent" : "text-ink"}>{l.label}</span>
              {active === l.code && <IconCheck size={14} className="shrink-0 text-accent" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// LangMenu is the picker in the desktop's top bar: a globe and the language's code,
// opening the list — where a reader looks for a language switch on a wide screen.
export function LangMenu() {
  const { t } = useTranslation();
  const active = currentLang();
  return (
    <Dropdown
      align="end"
      width={180}
      trigger={
        <span
          title={t("common.language")}
          className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-xs font-semibold text-ink-muted transition hover:bg-gray-100 hover:text-ink"
        >
          <IconGlobe size={15} />
          {active.toUpperCase()}
        </span>
      }
    >
      {LANGS.map((l) => (
        <DropdownItem key={l.code} onClick={() => void setLang(l.code as Lang)}>
          <span className="flex items-center justify-between gap-2">
            <span lang={l.code} className={active === l.code ? "font-medium text-accent" : ""}>
              {l.label}
            </span>
            {active === l.code && <IconCheck size={14} className="text-accent" />}
          </span>
        </DropdownItem>
      ))}
    </Dropdown>
  );
}
