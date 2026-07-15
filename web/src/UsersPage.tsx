import { useCallback, useEffect, useState } from "react";
import { getRegistrations, type RegistrationRequest } from "./api";
import { EventsPanel } from "./EventsPanel";
import { RegistrationsPanel } from "./RegistrationsPanel";
import { navigate, useRoute } from "./router";
import { StatsPanel } from "./StatsPanel";
import { Badge, cn } from "./ui";
import { UsersPanel } from "./UsersPanel";

// Statistics and the journal are both *about* end users — who spent how much
// traffic, and what was done to whom — so they live as sub-tabs of this section
// instead of eating two slots in the top nav. The "Заявки" tab appears only while
// the user bot is in moderation mode (or a leftover queue needs clearing).
type SubTab = "list" | "requests" | "stats" | "events";

export function UsersPage() {
  const seg = useRoute();
  const [reg, setReg] = useState<{
    moderation: boolean;
    requests: RegistrationRequest[];
  }>({ moderation: false, requests: [] });

  const loadReg = useCallback(
    () =>
      getRegistrations()
        .then((d) =>
          setReg({ moderation: !!d.moderation, requests: d.requests ?? [] }),
        )
        .catch(() => {}),
    [],
  );

  useEffect(() => {
    loadReg();
    // Poll so requests arriving via the bot surface (and the tab appears) without a
    // reload.
    const id = setInterval(loadReg, 20000);
    return () => clearInterval(id);
  }, [loadReg]);

  const showRequests = reg.moderation || reg.requests.length > 0;
  const tabs: { value: SubTab; label: string; count?: number }[] = [
    { value: "list", label: "Список" },
    ...(showRequests
      ? [
          {
            value: "requests" as SubTab,
            label: "Заявки",
            count: reg.requests.length,
          },
        ]
      : []),
    { value: "stats", label: "Статистика" },
    { value: "events", label: "Журнал" },
  ];

  const wanted = seg[1] as SubTab;
  const tab: SubTab = tabs.some((t) => t.value === wanted) ? wanted : "list";

  return (
    <div className="flex flex-col gap-4">
      <div className="no-scrollbar flex gap-1 overflow-x-auto border-b border-gray-200">
        {tabs.map((t) => (
          <button
            key={t.value}
            onClick={() =>
              navigate(t.value === "list" ? "users" : `users/${t.value}`)
            }
            className={cn(
              "flex items-center gap-2 whitespace-nowrap border-b-2 px-3 py-2 text-sm font-semibold transition",
              tab === t.value
                ? "border-brand-600 text-brand-800"
                : "border-transparent text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
            {t.count ? (
              <Badge color="orange" size="xs">
                {t.count}
              </Badge>
            ) : null}
          </button>
        ))}
      </div>

      <div key={tab} className="animate-fade-in">
        {tab === "list" && <UsersPanel />}
        {tab === "requests" && (
          <RegistrationsPanel requests={reg.requests} onReload={loadReg} />
        )}
        {tab === "stats" && <StatsPanel />}
        {tab === "events" && <EventsPanel />}
      </div>
    </div>
  );
}
