import { useTranslation } from "react-i18next";
import {
  approveRegistration,
  rejectRegistration,
  type RegistrationRequest,
} from "./api";
import { useAction } from "./hooks";
import { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, SettingCard } from "./ui";

function fmtDateTime(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// RegistrationsPanel is the "Requests" sub-tab: the moderated self-registration queue
// with approve/reject per request. It's presentational — the list and reload come
// from UsersPage (which owns the poll that drives the tab's visibility and count).
export function RegistrationsPanel({
  requests,
  onReload,
}: {
  requests: RegistrationRequest[];
  onReload: () => void;
}) {
  const { t } = useTranslation();
  const { busy, run } = useAction();

  const decide = (id: number, approve: boolean) =>
    run(async () => {
      await (approve ? approveRegistration(id) : rejectRegistration(id));
      notifySuccess(t(approve ? "reg.approved" : "reg.rejected"));
      onReload();
    }).catch((e) => notifyError(errMessage(e)));

  return (
    <SettingCard
      title={t("reg.title")}
      description={t("reg.description")}
    >
      {requests.length === 0 ? (
        <p className="text-sm text-ink-muted">{t("reg.empty")}</p>
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
                  {t("reg.approve")}
                </Button>
                <Button
                  size="sm"
                  variant="subtle"
                  color="red"
                  disabled={busy}
                  onClick={() => decide(r.id, false)}
                >
                  {t("reg.reject")}
                </Button>
              </span>
            </li>
          ))}
        </ul>
      )}
    </SettingCard>
  );
}
