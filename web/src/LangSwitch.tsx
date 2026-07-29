// The language picker. Two shapes of the same control: a row of pills for the
// screens that have no account menu (login, the first-run wizard, the forced
// password change) and a set of dropdown items for the dashboard's account menu.
import { useTranslation } from "react-i18next";
import { currentLang, LANGS, setLang, type Lang } from "./i18n";
import { cn, DropdownItem, IconCheck } from "./ui";

// LangPills sits in a corner of the unauthenticated screens — those have no menu
// to hide it in, and an admin who cannot read the login form cannot get far enough
// to find a setting.
export function LangPills({ className }: { className?: string }) {
  const { t } = useTranslation();
  const active = currentLang();

  return (
    <div
      className={cn("flex items-center gap-1", className)}
      role="group"
      aria-label={t("common.language")}
    >
      {LANGS.map((l) => (
        <button
          key={l.code}
          type="button"
          onClick={() => setLang(l.code as Lang)}
          aria-pressed={active === l.code}
          className={cn(
            "rounded-full px-2.5 py-1 text-xs font-medium transition",
            active === l.code
              ? "bg-brand-600 text-onaccent"
              : "text-ink-muted hover:bg-gray-100",
          )}
        >
          {l.label}
        </button>
      ))}
    </div>
  );
}

// LangChoice renders the same options as DropdownItems, so they inherit the menu's
// padding and hover, and closing the menu on pick is handled for us.
export function LangChoice() {
  const active = currentLang();

  return (
    <>
      {LANGS.map((l) => (
        <DropdownItem key={l.code} onClick={() => setLang(l.code as Lang)}>
          <span className="flex items-center justify-between gap-2">
            <span className={active === l.code ? "font-medium text-accent" : ""}>
              {l.label}
            </span>
            {active === l.code && <IconCheck size={14} className="text-accent" />}
          </span>
        </DropdownItem>
      ))}
    </>
  );
}
