import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getEventCatalog, listEvents } from "./api";
import { actionMeta, actorOptions, EventList } from "./events";
import { errMessage, notifyError } from "./notify";
import { Panel, Select } from "./ui";

// The global audit trail: every recorded action across all users, newest first,
// filterable by action and by who performed it. Those two are the whole filter set —
// the endpoint takes an action, an actor kind and a cursor, and nothing else — so
// there is no date range and no export here.
export function EventsPanel() {
  const { t } = useTranslation();
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
    <Panel
      title={t("events.title")}
      aside={
        <span className="min-w-0 text-xs text-ink-muted">
          {t("events.retention", { count: RETENTION_DAYS })}
        </span>
      }
    >
      <div className="flex flex-col gap-2.5 px-3.5 py-3">
        {/* Each select says what it is by what it shows ("Все события", "Кто
            угодно"), so a label above it would only repeat the value. */}
        <div className="flex flex-wrap gap-2">
          <div className="w-full sm:w-52">
            <Select value={action} onChange={setAction} data={actions} />
          </div>
          <div className="w-full sm:w-44">
            <Select value={actor} onChange={setActor} data={actorOptions()} />
          </div>
        </div>
      </div>
      <EventList load={load} showUser table />
    </Panel>
  );
}

// Mirrors model.UserEventRetentionDays — shown so the operator knows the trail is
// not forever.
const RETENTION_DAYS = 90;
