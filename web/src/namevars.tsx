import { useTranslation } from "react-i18next";

// The variables a connection name can carry, mirrored from model.NameVarList. Kept
// as a literal list rather than fetched: it changes when the Go side changes, and a
// name the server does not expand is left verbatim in the client's server list —
// visible, harmless, and obviously wrong, which is the failure mode to prefer.
export const NAME_VARS = [
  "{flag}",
  "{country}",
  "{server}",
  "{user}",
  "{used}",
  "{left}",
  "{total}",
  "{expire}",
  "{days}",
] as const;

// NameVarsHint explains the variables under a name field and lets the operator paste
// one in rather than remember its spelling.
export function NameVarsHint({ onInsert }: { onInsert: (v: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-xs text-ink-muted">{t("nameVars.hint")}</p>
      <div className="flex flex-wrap gap-1">
        {NAME_VARS.map((v) => (
          <button
            key={v}
            type="button"
            onClick={() => onInsert(v)}
            title={t(`nameVars.${v.slice(1, -1)}` as "nameVars.flag")}
            className="rounded border border-gray-200 bg-white px-1.5 py-0.5 font-mono text-[11px] text-ink-muted transition hover:border-brand-400 hover:text-ink"
          >
            {v}
          </button>
        ))}
      </div>
      <p className="text-xs text-ink-muted">{t("nameVars.churn")}</p>
    </div>
  );
}
