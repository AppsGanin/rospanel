import { EventsPanel } from "./EventsPanel";
import { navigate, useRoute } from "./router";
import { StatsPanel } from "./StatsPanel";
import { cn } from "./ui";
import { UsersPanel } from "./UsersPanel";

// Statistics and the journal are both *about* end users — who spent how much
// traffic, and what was done to whom — so they live as sub-tabs of this section
// instead of eating two slots in the top nav.
const SUBTABS = [
  { value: "list", label: "Список" },
  { value: "stats", label: "Статистика" },
  { value: "events", label: "Журнал" },
] as const;

type SubTab = (typeof SUBTABS)[number]["value"];

export function UsersPage() {
  const seg = useRoute();
  const tab = (SUBTABS.find((t) => t.value === seg[1])?.value ??
    "list") as SubTab;
  return (
    <div className="flex flex-col gap-4">
      <div className="no-scrollbar flex gap-1 overflow-x-auto border-b border-gray-200">
        {SUBTABS.map((t) => (
          <button
            key={t.value}
            onClick={() =>
              navigate(t.value === "list" ? "users" : `users/${t.value}`)
            }
            className={cn(
              "whitespace-nowrap border-b-2 px-3 py-2 text-sm font-semibold transition",
              tab === t.value
                ? "border-brand-600 text-brand-800"
                : "border-transparent text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div key={tab} className="animate-fade-in">
        {tab === "list" && <UsersPanel />}
        {tab === "stats" && <StatsPanel />}
        {tab === "events" && <EventsPanel />}
      </div>
    </div>
  );
}
