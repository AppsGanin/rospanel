import { useState } from "react";
import { setupPassword } from "./api";
import { useAction } from "./hooks";
import { BrandLogo } from "./Logo";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, Card, PasswordInput } from "./ui";

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
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const { busy, run } = useAction();

  const submit = () => {
    if (password.length < 8) {
      return notifyError("Пароль должен быть не короче 8 символов");
    }
    if (password !== confirm) {
      return notifyError("Пароли не совпадают");
    }
    run(async () => {
      try {
        await setupPassword(password);
        notifySuccess("Пароль изменён");
        onDone();
      } catch (e) {
        notifyError(errMessage(e));
      }
    });
  };

  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-sm animate-fade-in-up p-6">
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <div className="mb-1 flex justify-center">
            <BrandLogo size={32} />
          </div>
          <p className="text-center text-sm text-ink-muted">
            Вы вошли как <b className="text-ink">{username}</b> с временным
            паролем. Придумайте свой — до этого панель закрыта.
          </p>
          <PasswordInput
            label="Новый пароль"
            value={password}
            onChange={setPassword}
            autoFocus
          />
          <PasswordInput
            label="Повторите пароль"
            value={confirm}
            onChange={setConfirm}
          />
          <Button type="submit" loading={busy} fullWidth>
            Сохранить и войти
          </Button>
        </form>
      </Card>
    </div>
  );
}
