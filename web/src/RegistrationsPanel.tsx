import { useTranslation } from "react-i18next";
import {
  approveRegistration,
  rejectRegistration,
  type RegistrationRequest,
} from "./api";
import { useAction, useShowMore } from "./hooks";
import { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, EmptyState, Mono, Panel, ShowMore } from "./ui";

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
  // The queue is unbounded — nothing trims it but an operator working through it —
  // so a backlog left alone for a week would otherwise render in full.
  const page = useShowMore(requests);

  const decide = (id: number, approve: boolean) =>
    run(async () => {
      await (approve ? approveRegistration(id) : rejectRegistration(id));
      notifySuccess(t(approve ? "reg.approved" : "reg.rejected"));
      onReload();
    }).catch((e) => notifyError(errMessage(e)));

  return (
    <Panel title={t("reg.title")}>
      <p className="border-b border-brand-600/10 px-3.5 py-3 text-xs leading-relaxed text-ink-muted">
        {t("reg.description")}
      </p>
      {requests.length === 0 ? (
        <EmptyState title={t("reg.empty")} body={t("reg.emptyHint")} />
      ) : (
        <>
          {page.shown.map((r) => (
            <div
              key={r.id}
              className="flex flex-wrap items-center gap-2.5 border-b border-gray-100 px-3.5 py-2.25"
            >
              {/* Amber, not green: a request is a thing waiting on the operator. */}
              <span className="size-2 shrink-0 rounded-full bg-warning" />
              <span className="truncate text-[13px] font-semibold text-ink">
                {r.name}
              </span>
              <span className="min-w-0 flex-1 truncate text-xs text-ink-muted">
                Telegram
              </span>
              <Mono className="shrink-0 text-[11px] text-ink-muted">
                {r.chat_id}
              </Mono>
              <Mono className="shrink-0 text-[11px] text-ink-muted">
                {fmtDateTime(r.created_at)}
              </Mono>
              <span className="flex shrink-0 gap-2">
                <Button
                  size="xs"
                  disabled={busy}
                  onClick={() => decide(r.id, true)}
                >
                  {t("reg.approve")}
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  color="red"
                  disabled={busy}
                  onClick={() => decide(r.id, false)}
                >
                  {t("reg.reject")}
                </Button>
              </span>
            </div>
          ))}
          <ShowMore
            rest={page.rest}
            onClick={page.showMore}
            className="p-3.5"
          />
        </>
      )}
    </Panel>
  );
}
