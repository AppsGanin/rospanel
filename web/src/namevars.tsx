import { useTranslation } from "react-i18next";

// The variables a connection name can carry, mirrored from model.NameVarList. Kept
// as a literal list rather than fetched: it changes when the Go side changes, and a
// name the server does not expand is left verbatim in the client's server list —
// visible, harmless, and obviously wrong, which is the failure mode to prefer.
export const NAME_VARS = [
  "{server}",
  "{user}",
  "{used}",
  "{left}",
  "{total}",
  "{expire}",
  "{days}",
] as const;

type NameVar = (typeof NAME_VARS)[number];

// STATIC_NAME_VARS is what a name may carry when it goes into a file the user keeps —
// an AmneziaWG config, a WireGuard config behind a TURN relay. That name is written
// when the file is downloaded and never updated again, so a variable that moves with
// traffic or with the term would sit frozen at whatever it read that day.
export const STATIC_NAME_VARS: readonly NameVar[] = ["{server}", "{user}"];

// The variables that change as the user spends their quota. Their warning is beside
// the point when they are not on offer, and the static line takes its place.
const CHURNING: readonly NameVar[] = ["{used}", "{left}", "{days}"];

// NameVarsHint explains the variables under a name field and lets the operator paste
// one in rather than remember its spelling.
export function NameVarsHint({
  onInsert,
  vars = NAME_VARS,
}: {
  onInsert: (v: string) => void;
  vars?: readonly NameVar[];
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-xs text-ink-muted">{t("nameVars.hint")}</p>
      <div className="flex flex-wrap gap-1">
        {vars.map((v) => (
          <button
            key={v}
            type="button"
            onClick={() => onInsert(v)}
            title={t(`nameVars.${v.slice(1, -1)}` as "nameVars.server")}
            className="rounded border border-gray-200 bg-white px-1.5 py-0.5 font-mono text-[11px] text-ink-muted transition hover:border-brand-400 hover:text-ink"
          >
            {v}
          </button>
        ))}
      </div>
      <p className="text-xs text-ink-muted">
        {vars.some((v) => CHURNING.includes(v))
          ? t("nameVars.churn")
          : t("nameVars.static")}
      </p>
    </div>
  );
}
