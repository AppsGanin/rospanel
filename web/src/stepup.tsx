import { createContext, type ReactNode, useContext } from "react";
import { useTranslation } from "react-i18next";
import { PasswordInput, TextInput } from "./ui";

// Whether the signed-in admin has a second factor, published once at the top the same
// way the role is (see role.tsx). It decides whether a destructive-action dialog asks
// for a code as well as a password.
//
// Cosmetic, like the role: the server re-checks both credentials on every one of
// these actions, so a dialog that asks for too little is refused rather than obeyed.
// The default is true — a wrong "no second factor" guess hides the field the server
// is about to demand, which reads as an action that simply cannot be completed.
const TotpCtx = createContext(true);

export function TotpProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  return <TotpCtx.Provider value={enabled}>{children}</TotpCtx.Provider>;
}

export const useTotpEnabled = () => useContext(TotpCtx);

// StepUp is what an irreversible action has to be re-authorised with: the admin's
// current password, plus a fresh authenticator code when they have one bound.
export interface StepUp {
  password: string;
  code: string;
}

export const EMPTY_STEP_UP: StepUp = { password: "", code: "" };

// stepUpReady reports whether the form has enough to be worth sending. Six digits is
// the length every authenticator produces; anything shorter is a half-typed code, and
// sending it would burn the attempt on a certain refusal.
export function stepUpReady(v: StepUp, totpEnabled: boolean): boolean {
  return v.password !== "" && (!totpEnabled || v.code.length === 6);
}

// StepUpFields renders the credentials an irreversible action asks for. One component
// so the dialogs cannot drift apart on which of them asks for the second factor.
export function StepUpFields({
  value,
  onChange,
}: {
  value: StepUp;
  onChange: (v: StepUp) => void;
}) {
  const { t } = useTranslation();
  const totp = useTotpEnabled();
  return (
    <div className="mt-3 flex flex-col gap-3">
      <PasswordInput
        label={t("creds.currentPassword")}
        value={value.password}
        onChange={(password) => onChange({ ...value, password })}
      />
      {totp && (
        <div className="flex flex-col gap-1">
          <TextInput
            label={t("stepUp.code")}
            value={value.code}
            placeholder="000000"
            inputMode="numeric"
            autoComplete="one-time-code"
            onChange={(code) => onChange({ ...value, code: code.replace(/\D/g, "").slice(0, 6) })}
          />
          <p className="text-xs text-ink-muted">{t("stepUp.codeHint")}</p>
        </div>
      )}
    </div>
  );
}
