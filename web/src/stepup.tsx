import { createContext, type ReactNode, useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import { Button, Modal, PasswordInput, TextInput } from "./ui";

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
  // A code from the authenticator of a BACKUP being restored, when its admins had one
  // (verifyBackupTOTP). Empty everywhere else.
  backupCode: string;
}

export const EMPTY_STEP_UP: StepUp = { password: "", code: "", backupCode: "" };

// stepUpReady reports whether the form has enough to be worth sending. Six digits is
// the length every authenticator produces; anything shorter is a half-typed code, and
// sending it would burn the attempt on a certain refusal.
export function stepUpReady(v: StepUp, totpEnabled: boolean): boolean {
  return v.password !== "" && (!totpEnabled || v.code.length === 6);
}

// askReady is stepUpReady for the dialog, which can also skip the password and ask for
// a backup's code. The backup's code may be left empty when this admin's own code is
// there: the server tries that one against the backup too, so rolling a panel back to
// its own backup takes one code, not the same six digits typed twice.
function askReady(v: StepUp, ask: StepUpAsk, hasTotp: boolean): boolean {
  if (ask.password !== false && v.password === "") return false;
  const ownCode = hasTotp && !!ask.withCode;
  if (ownCode && v.code.length !== 6) return false;
  if (ask.backupCode && v.backupCode.length !== 6 && !(ownCode && v.code.length === 6)) {
    return false;
  }
  return true;
}

// StepUpFields renders the credentials an irreversible action asks for. One component
// so the dialogs cannot drift apart on which of them asks for the second factor.
export function StepUpFields({
  value,
  onChange,
  withCode,
  password = true,
  backupCode,
}: {
  value: StepUp;
  onChange: (v: StepUp) => void;
  // Only two endpoints demand a fresh second factor (restore/factory reset and
  // deleting a node — verifyStepUpTOTP on the server). Asking for a code where the
  // server ignores it would block the button on an account that has 2FA.
  withCode?: boolean;
  // false in the first-run wizard, which has no admin of its own to ask for one.
  password?: boolean;
  // Ask for a code from the backup being restored, too.
  backupCode?: boolean;
}) {
  const { t } = useTranslation();
  const totp = useTotpEnabled() && !!withCode;
  const digits = (v: string) => v.replace(/\D/g, "").slice(0, 6);
  return (
    <div className="flex flex-col gap-3">
      {password && (
        <PasswordInput
          label={t("creds.currentPassword")}
          value={value.password}
          onChange={(password) => onChange({ ...value, password })}
        />
      )}
      {totp && (
        <div className="flex flex-col gap-1">
          <TextInput
            label={t("stepUp.code")}
            value={value.code}
            placeholder="000000"
            inputMode="numeric"
            autoComplete="one-time-code"
            onChange={(code) => onChange({ ...value, code: digits(code) })}
          />
          <p className="text-xs text-ink-muted">{t("stepUp.codeHint")}</p>
        </div>
      )}
      {backupCode && (
        <div className="flex flex-col gap-1">
          <TextInput
            label={t("restore.backupCode")}
            value={value.backupCode}
            placeholder="000000"
            inputMode="numeric"
            autoComplete="one-time-code"
            onChange={(v) => onChange({ ...value, backupCode: digits(v) })}
          />
          <p className="text-xs text-ink-muted">
            {t("restore.backupCodeHint")}
            {totp && ` ${t("restore.backupCodeSameHint")}`}
          </p>
        </div>
      )}
    </div>
  );
}

// useStepUpDialog is how an action asks to be re-authorised: its own dialog, opened
// at the moment it is needed, rather than a password field parked at the bottom of
// every form that might one day do something irreversible.
//
// Same shape as useConfirm: await ask(...) resolves to the credentials, or null when
// the operator backs out. Render the returned node once, anywhere in the component.
export function useStepUpDialog() {
  const hasTotp = useTotpEnabled();
  const [req, setReq] = useState<
    | (StepUpAsk & { resolve: (v: StepUp | null) => void })
    | null
  >(null);
  const [value, setValue] = useState<StepUp>(EMPTY_STEP_UP);
  const [busy, setBusy] = useState(false);

  const ask = (opts: StepUpAsk = {}) => {
    setValue(EMPTY_STEP_UP);
    setBusy(false);
    return new Promise<StepUp | null>((resolve) => setReq({ ...opts, resolve }));
  };
  const close = (v: StepUp | null) => {
    req?.resolve(v);
    setReq(null);
  };

  const stepUpNode = (
    <Modal
      open={!!req}
      onClose={() => close(null)}
      title={req?.title ?? i18n.t("stepUp.title")}
      subtitle={req?.body}
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={() => close(null)}>
            {i18n.t("common.cancel")}
          </Button>
          <Button
            color={req?.danger ? "red" : "brand"}
            loading={busy}
            disabled={!req || !askReady(value, req, hasTotp)}
            onClick={() => {
              setBusy(true);
              close(value);
            }}
          >
            {req?.confirmLabel ?? i18n.t("common.confirm")}
          </Button>
        </div>
      }
    >
      <StepUpFields
        value={value}
        onChange={setValue}
        withCode={req?.withCode}
        password={req?.password}
        backupCode={req?.backupCode}
      />
    </Modal>
  );

  return { ask, stepUpNode };
}

// What the dialog says while it asks. The action names itself here — "Удалить admin",
// not "OK" — because this is the last screen before it happens.
export interface StepUpAsk {
  title?: string;
  body?: string;
  confirmLabel?: string;
  danger?: boolean;
  // Ask for an authenticator code as well — for the two actions whose endpoint
  // checks one (verifyStepUpTOTP).
  withCode?: boolean;
  // Ask for the password. Default true; the first-run wizard's restore passes false.
  password?: boolean;
  // Ask for a code from the backup being restored — when its owner/admins had 2FA.
  backupCode?: boolean;
}
