import { useTranslation } from "react-i18next";
import { AbuseSettings } from "./AbuseSettings";
import { ApiSettings } from "./ApiSettings";
import { BillingPanel } from "./BillingPanel";
import { BrandingSettings } from "./BrandingSettings";
import { GeneralSettings } from "./GeneralSettings";
import { navigate, useRoute } from "./router";
import { SubscriptionsPanel } from "./SubscriptionsPanel";
import { TelegramSettings } from "./TelegramSettings";
import type { Perm } from "./api";
import { useIsOwner, usePerms } from "./role";
import { cn, ReadOnly } from "./ui";

// Everything server-specific (connections/protocols, domain, routing, DNS, decoy)
// moved to the per-server cards on the "Servers" page: each server (the master
// included) owns its own, edited from its card rather than as global tabs here.
// Each tab and the permissions that show it: any one of `view` opens the tab, and
// without `manage` it is shown read-only. General gathers several sections and gates
// its blocks itself; the API tab splits keys from webhooks inside.
const SUBTABS: {
  value: string;
  label:
    | "settings.tabGeneral"
    | "settings.tabBranding"
    | "settings.tabSubscriptions"
    | "settings.tabTelegram"
    | "settings.tabBilling"
    | "settings.tabAbuse"
    | "settings.tabApi";
  view: Perm[];
  manage?: Perm;
  // The owner's alone, whatever a role holds (the bots — see panelMux).
  ownerOnly?: boolean;
}[] = [
  {
    value: "general",
    label: "settings.tabGeneral",
    view: ["settings.view", "security.view", "system.update"],
  },
  { value: "branding", label: "settings.tabBranding", view: ["settings.view"], manage: "settings.manage" },
  { value: "subscriptions", label: "settings.tabSubscriptions", view: ["settings.view"], manage: "settings.manage" },
  { value: "telegram", label: "settings.tabTelegram", view: [], ownerOnly: true },
  { value: "billing", label: "settings.tabBilling", view: ["billing.view"] },
  { value: "abuse", label: "settings.tabAbuse", view: ["security.view"], manage: "security.manage" },
  { value: "api", label: "settings.tabApi", view: ["api.manage", "webhooks.manage"] },
];


// The admin roster deliberately lives outside this panel — it's the owner's own
// business, not a setting of the VPN — and hangs off the account menu instead.
// See AdminsSettings, rendered by Dashboard on the "admins" route.
export function SettingsPanel() {
  const { t: tr } = useTranslation();
  const seg = useRoute();
  const held = usePerms();
  const isOwner = useIsOwner();
  const tabs = SUBTABS.filter((t) =>
    t.ownerOnly ? isOwner : t.view.some((p) => held.has(p)),
  );
  const current = tabs.find((t) => t.value === seg[1]) ?? tabs[0];
  const tab = current?.value ?? "general";
  const readOnly = !!current?.manage && !held.has(current.manage);
  return (
    // One section for the screen, the same shape the users page has: a header band, the
    // tab strip, and a content area that owns the scroll — so each section's own save
    // bar can stick to the bottom of the screen instead of floating over the page.
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-brand-600/10 bg-white">
      <div className="flex min-h-14 items-center gap-3 border-b border-brand-600/10 px-5 py-2">
        <h2 className="text-base font-semibold text-ink">{tr("nav.settings")}</h2>
      </div>

      <div className="no-scrollbar flex gap-0.5 overflow-x-auto border-b border-brand-600/10 px-5">
        {tabs.map((t) => (
          <button
            type="button"
            key={t.value}
            onClick={() =>
              navigate(
                t.value === tabs[0]?.value ? "settings" : `settings/${t.value}`,
              )
            }
            className={cn(
              "whitespace-nowrap border-b-2 px-3 py-2.5 text-[13px] font-semibold transition",
              tab === t.value
                ? "border-brand-600 text-ink"
                : "border-transparent text-ink-muted hover:text-ink",
            )}
          >
            {tr(t.label)}
          </button>
        ))}
      </div>

      <div
        key={tab}
        className="flex min-h-0 flex-1 animate-fade-in flex-col gap-3.5 overflow-y-auto p-5"
      >
        <ReadOnly when={readOnly}>
          {tab === "general" && <GeneralSettings />}
          {tab === "branding" && <BrandingSettings />}
          {tab === "subscriptions" && <SubscriptionsPanel />}
          {tab === "telegram" && <TelegramSettings />}
          {tab === "billing" && <BillingPanel />}
          {tab === "abuse" && <AbuseSettings />}
          {tab === "api" && <ApiSettings />}
        </ReadOnly>
      </div>
    </div>
  );
}
