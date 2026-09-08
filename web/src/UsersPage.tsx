import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { exportUsersURL, getRegistrations, type RegistrationRequest } from "./api";
import { BroadcastPanel } from "./BroadcastPanel";
import { EventsPanel } from "./EventsPanel";
import { GroupsPanel } from "./GroupsPanel";
import { RegistrationsPanel } from "./RegistrationsPanel";
import { useIsAdmin } from "./role";
import { PaymentsPage } from "./PaymentsPage";
import { navigate, useRoute } from "./router";
import { StatsPanel } from "./StatsPanel";
import { cn, IconButton, IconExport, IconImport, IconPlus, Mono } from "./ui";
import { UsersPanel } from "./UsersPanel";

// Statistics and the journal are both *about* end users — who spent how much
// traffic, and what was done to whom — so they live as sub-tabs of this section
// instead of eating two slots in the top nav. The "Requests" tab appears only while
// the user bot is in moderation mode (or a leftover queue needs clearing).
type SubTab =
  | "list"
  | "requests"
  | "groups"
  | "broadcast"
  | "payments"
  | "stats"
  | "events";

export function UsersPage({
  userBotEnabled,
  billingEnabled,
}: {
  userBotEnabled: boolean;
  billingEnabled: boolean;
}) {
  const { t } = useTranslation();
  const seg = useRoute();
  // The list's three actions live in this header, above the tabs, so they read as
  // "what you can do on this screen" rather than as another toolbar control. Their
  // open/closed state is held here for the same reason; UsersPanel draws the dialogs.
  const [addOpen, setAddOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [total, setTotal] = useState(0);
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

  const isAdmin = useIsAdmin();
  const showRequests = reg.moderation || reg.requests.length > 0;
  const tabs: { value: SubTab; label: string; count?: number }[] = [
    { value: "list", label: t("users.tabList") },
    ...(showRequests
      ? [
          {
            value: "requests" as SubTab,
            label: t("users.tabRequests"),
            count: reg.requests.length,
          },
        ]
      : []),
    // Broadcasts live here rather than at the top level: the audience is the bot's
    // users, and composing one is something you do while looking at them. Hidden
    // without the user bot, which is what actually delivers them — the server would
    // refuse anyway, and a tab that always errors is worse than no tab.
    ...(isAdmin && userBotEnabled
      ? [{ value: "broadcast" as SubTab, label: t("users.tabBroadcast") }]
      : []),
    // Payments are about what users pay for, so they belong beside the users rather
    // than as a separate destination in the top menu.
    ...(isAdmin && billingEnabled
      ? [{ value: "payments" as SubTab, label: t("users.tabPayments") }]
      : []),
    // Access groups gate which connections a user may use; managing them lives beside
    // the users whose membership they govern. Admin-and-up, like the other management
    // sub-tabs.
    ...(isAdmin
      ? [{ value: "groups" as SubTab, label: t("users.tabGroups") }]
      : []),
    { value: "stats", label: t("users.tabStats") },
    { value: "events", label: t("users.tabEvents") },
  ];

  const wanted = seg[1] as SubTab;
  const tab: SubTab = tabs.some((t) => t.value === wanted) ? wanted : "list";

  return (
    // One section for the whole screen: the tab strip, the toolbar, the rows and the
    // footer are its bands, divided by rules rather than floated apart. That is what
    // makes the bulk bar the panel's bottom edge instead of a pill under the list.
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-brand-600/10 bg-white">
      <div className="flex min-h-14 items-center gap-3 border-b border-brand-600/10 px-5 py-2">
        <h2 className="text-base font-semibold text-ink">{t("nav.users")}</h2>
        {total > 0 && <Mono className="text-xs text-ink-muted">{total}</Mono>}
        {/* Icons, not words: three labelled buttons were the widest thing in this band,
            for actions whose shapes are unambiguous. The words live on as the
            accessible name and the hover title. */}
        {tab === "list" && (
          <div className="ml-auto flex items-center gap-1">
            <IconButton
              title={t("importUsers.buttonHint")}
              onClick={() => setImportOpen(true)}
            >
              <IconImport />
            </IconButton>
            {/* A plain link, not a fetch: the file is an attachment the browser saves,
                and it carries every credential — no reason for it to pass through the
                SPA. */}
            <IconButton href={exportUsersURL()} title={t("importUsers.exportHint")}>
              <IconExport />
            </IconButton>
            {/* Last, and the only filled one: the primary action ends the row. */}
            <IconButton
              variant="filled"
              color="brand"
              title={t("usersPanel.addUser")}
              onClick={() => setAddOpen(true)}
            >
              <IconPlus />
            </IconButton>
          </div>
        )}
      </div>

      <div className="no-scrollbar flex gap-0.5 overflow-x-auto border-b border-brand-600/10 px-5">
        {tabs.map((t) => (
          <button
            key={t.value}
            onClick={() =>
              navigate(t.value === "list" ? "users" : `users/${t.value}`)
            }
            className={cn(
              "flex items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-[13px] font-semibold transition",
              tab === t.value
                ? "border-brand-600 text-ink"
                : "border-transparent text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
            {t.count ? (
              <span className="warning-tint rounded-sm px-1.5 py-px font-mono text-[11px] text-warning">
                {t.count}
              </span>
            ) : null}
          </button>
        ))}
      </div>

      <div key={tab} className="flex min-h-0 flex-1 flex-col animate-fade-in">
        {tab === "list" && (
          <UsersPanel
            userBotEnabled={userBotEnabled}
            addOpen={addOpen}
            onAddOpen={setAddOpen}
            importOpen={importOpen}
            onImportOpen={setImportOpen}
            onTotal={setTotal}
          />
        )}
        {tab !== "list" && <div className="min-h-0 flex-1 overflow-y-auto p-5">
        {tab === "requests" && (
          <RegistrationsPanel requests={reg.requests} onReload={loadReg} />
        )}
        {tab === "broadcast" && <BroadcastPanel />}
        {tab === "payments" && <PaymentsPage />}
        {tab === "groups" && <GroupsPanel />}
        {tab === "stats" && <StatsPanel />}
        {tab === "events" && <EventsPanel />}
        </div>}
      </div>
    </div>
  );
}
