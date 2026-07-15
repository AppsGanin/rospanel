import {
  approveRegistration,
  rejectRegistration,
  type RegistrationRequest,
} from "./api";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, SettingCard } from "./ui";

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// RegistrationsPanel is the "Заявки" sub-tab: the moderated self-registration queue
// with approve/reject per request. It's presentational — the list and reload come
// from UsersPage (which owns the poll that drives the tab's visibility and count).
export function RegistrationsPanel({
  requests,
  onReload,
}: {
  requests: RegistrationRequest[];
  onReload: () => void;
}) {
  const { busy, run } = useAction();

  const decide = (id: number, approve: boolean) =>
    run(async () => {
      await (approve ? approveRegistration(id) : rejectRegistration(id));
      notifySuccess(approve ? "Заявка одобрена" : "Заявка отклонена");
      onReload();
    }).catch((e) => notifyError(errMessage(e)));

  return (
    <SettingCard
      title="Заявки на регистрацию"
      description="Модерация самостоятельной регистрации: аккаунт создаётся только после одобрения. Одобрить или отклонить также можно кнопками в админ-боте."
    >
      {requests.length === 0 ? (
        <p className="text-sm text-ink-muted">Новых заявок нет.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {requests.map((r) => (
            <li
              key={r.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-gray-200 px-3 py-2.5 text-sm"
            >
              <span className="min-w-0">
                <b className="text-ink">{r.name}</b>
                <span className="ml-2 text-xs text-ink-muted">
                  Telegram ID {r.chat_id} · {fmtDateTime(r.created_at)}
                </span>
              </span>
              <span className="flex gap-2">
                <Button
                  size="sm"
                  disabled={busy}
                  onClick={() => decide(r.id, true)}
                >
                  Одобрить
                </Button>
                <Button
                  size="sm"
                  variant="subtle"
                  color="red"
                  disabled={busy}
                  onClick={() => decide(r.id, false)}
                >
                  Отклонить
                </Button>
              </span>
            </li>
          ))}
        </ul>
      )}
    </SettingCard>
  );
}
