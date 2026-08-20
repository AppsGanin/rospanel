import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getEventCatalog, listEvents } from "./api";
import { actionMeta, actorOptions, EventList } from "./events";
import { useViewMode } from "./hooks";
import { errMessage, notifyError } from "./notify";
import { Select, SettingCard, ViewSwitch } from "./ui";

// The global audit trail: every recorded action across all users, newest first,
// filterable by action and by who performed it.
export function EventsPanel() {
  const { t } = useTranslation();
  const [view, setView] = useViewMode("events");
  const [keys, setKeys] = useState<string[]>([]);
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");

  // The action list comes from the server so it stays in lockstep with the Go
  // catalog rather than being duplicated here. Only the KEYS are used: the label
  // is looked up in the dictionaries, so the filter follows the panel's language
  // instead of whatever the server happens to speak.
  useEffect(() => {
    getEventCatalog()
      .then((cat) => setKeys(cat.map((e) => e.key)))
      .catch((e) => notifyError(errMessage(e)));
  }, []);

  const actions = [
    { value: "", label: t("events.allActions") },
    ...keys.map((k) => ({ value: k, label: actionMeta(k).label })),
  ];

  // Re-created whenever a filter changes — that identity change is what makes
  // EventList refetch from the newest page.
  const load = useCallback(
    (before: number) => listEvents({ action, actor, before }),
    [action, actor],
  );

  return (
    <SettingCard
      title={t("events.title")}
      description={t("events.description", { days: RETENTION_DAYS })}
      action={
        <ViewSwitch
          value={view}
          onChange={setView}
          tableLabel={t("usersPanel.viewTable")}
          cardsLabel={t("usersPanel.viewCards")}
        />
      }
      stackAction
    >
      <div className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Select
            label={t("events.filterAction")}
            value={action}
            onChange={setAction}
            data={actions}
          />
          <Select
            label={t("events.filterActor")}
            value={actor}
            onChange={setActor}
            data={actorOptions()}
          />
        </div>
        <EventList load={load} showUser table={view === "table"} />
      </div>
    </SettingCard>
  );
}

// Mirrors model.UserEventRetentionDays — shown so the operator knows the trail is
// not forever.
const RETENTION_DAYS = 90;
