import { useCallback, useEffect, useState } from "react";
import { getEventCatalog, listEvents } from "./api";
import { ACTOR_OPTIONS, EventList } from "./events";
import { errMessage, notifyError } from "./notify";
import { Select, SettingCard } from "./ui";

// The global audit trail: every recorded action across all users, newest first,
// filterable by action and by who performed it.
export function EventsPanel() {
  const [actions, setActions] = useState<{ value: string; label: string }[]>([
    { value: "", label: "Все события" },
  ]);
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");

  // The action list comes from the server so it stays in lockstep with the Go
  // catalog rather than being duplicated here.
  useEffect(() => {
    getEventCatalog()
      .then((cat) =>
        setActions([
          { value: "", label: "Все события" },
          ...cat.map((e) => ({ value: e.key, label: e.label })),
        ]),
      )
      .catch((e) => notifyError(errMessage(e)));
  }, []);

  // Re-created whenever a filter changes — that identity change is what makes
  // EventList refetch from the newest page.
  const load = useCallback(
    (before: number) => listEvents({ action, actor, before }),
    [action, actor],
  );

  return (
    <SettingCard
      title="Журнал действий"
      description={`Что происходило с пользователями: действия админов, оплаты, саморегистрации и системные события. Хранится ${RETENTION_DAYS} дней.`}
    >
      <div className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Select
            label="Событие"
            value={action}
            onChange={setAction}
            data={actions}
          />
          <Select
            label="Кто"
            value={actor}
            onChange={setActor}
            data={ACTOR_OPTIONS}
          />
        </div>
        <EventList load={load} showUser />
      </div>
    </SettingCard>
  );
}

// Mirrors model.UserEventRetentionDays — shown so the operator knows the trail is
// not forever.
const RETENTION_DAYS = 90;
