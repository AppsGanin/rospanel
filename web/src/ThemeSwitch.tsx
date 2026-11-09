// The light/dark picker: follow the system, or hold one theme in this browser.
// A globe-like trigger in the desktop's top bar and a row in the phone's account
// sheet, the same two shapes as the language picker.
import { useTranslation } from "react-i18next";
import { useBrand, type ThemeMode } from "./brand";
import { cn, Dropdown, DropdownItem, IconCheck, IconMoon, IconSun } from "./ui";

const MODES: ThemeMode[] = ["auto", "light", "dark"];

function useModes() {
  const { t } = useTranslation();
  const label: Record<ThemeMode, string> = {
    auto: t("common.themeAuto"),
    light: t("common.themeLight"),
    dark: t("common.themeDark"),
  };
  return label;
}

export function ThemeMenu() {
  const { t } = useTranslation();
  const brand = useBrand();
  const label = useModes();
  const Icon = brand.dark ? IconMoon : IconSun;
  return (
    <Dropdown
      align="end"
      width={180}
      trigger={
        <span
          title={t("common.theme")}
          aria-label={t("common.theme")}
          className="inline-flex size-8 items-center justify-center rounded-lg text-ink-muted transition hover:bg-gray-100 hover:text-ink"
        >
          <Icon size={15} />
        </span>
      }
    >
      {MODES.map((m) => (
        <DropdownItem key={m} onClick={() => brand.setMode(m)}>
          <span className="flex items-center justify-between gap-2">
            <span className={brand.mode === m ? "font-medium text-accent" : ""}>{label[m]}</span>
            {brand.mode === m && <IconCheck size={14} className="text-accent" />}
          </span>
        </DropdownItem>
      ))}
    </Dropdown>
  );
}

// ThemeRow is the picker in the phone's account sheet: three choices in one row.
export function ThemeRow() {
  const { t } = useTranslation();
  const brand = useBrand();
  const label = useModes();
  return (
    <div className="flex items-center gap-2 border-t border-gray-100 px-3.5 py-2">
      <span className="flex-1 text-[13px] font-medium text-ink">{t("common.theme")}</span>
      <div className="flex gap-1" role="group" aria-label={t("common.theme")}>
        {MODES.map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => brand.setMode(m)}
            aria-pressed={brand.mode === m}
            className={cn(
              "whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium transition",
              brand.mode === m ? "bg-brand-600 text-onbrand" : "text-ink-muted active:bg-gray-100",
            )}
          >
            {label[m]}
          </button>
        ))}
      </div>
    </div>
  );
}
