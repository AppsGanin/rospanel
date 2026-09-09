import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { setupPassword } from "./api";
import { AuthShell } from "./AuthShell";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, PasswordInput } from "./ui";

// The screen a colleague lands on at their first sign-in, while they still hold a
// password the owner picked for them and sent over a chat window. Until they replace
// it the server refuses everything else (requireAuth's must-change gate), so this is
// not a suggestion — it is the only door out, and there is deliberately no way to
// skip it.
export function ForcePassword({
  username,
  onDone,
}: {
  username: string;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const { busy, run } = useAction();

  const submit = () => {
    if (password.length < 8) {
      return notifyError(t("password.tooShort"));
    }
    if (password !== confirm) {
      return notifyError(t("password.mismatch"));
    }
    run(async () => {
      try {
        await setupPassword(password);
        notifySuccess(t("password.changed"));
        onDone();
      } catch (e) {
        notifyError(errMessage(e));
      }
    });
  };

  return (
    <AuthShell>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <p className="text-xs leading-relaxed text-ink-muted">
          <Trans
            i18nKey="password.forcedIntro"
            values={{ username }}
            components={{ b: <b className="text-ink" /> }}
          />
        </p>
        <PasswordInput
          label={t("password.new")}
          value={password}
          onChange={setPassword}
          autoFocus
        />
        <PasswordInput
          label={t("password.repeat")}
          value={confirm}
          onChange={setConfirm}
        />
        <Button type="submit" loading={busy} fullWidth>
          {t("password.saveAndEnter")}
        </Button>
      </form>
    </AuthShell>
  );
}
