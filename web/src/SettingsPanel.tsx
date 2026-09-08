import { useTranslation } from "react-i18next";
import { AbuseSettings } from "./AbuseSettings";
import { ApiSettings } from "./ApiSettings";
import { BillingPanel } from "./BillingPanel";
import { BrandingSettings } from "./BrandingSettings";
import { GeneralSettings } from "./GeneralSettings";
import { navigate, useRoute } from "./router";
import { SubscriptionsPanel } from "./SubscriptionsPanel";
import { TelegramSettings } from "./TelegramSettings";
import { cn } from "./ui";
import { WebhooksSettings } from "./WebhooksSettings";

// Everything server-specific (connections/protocols, domain, routing, DNS, decoy)
// moved to the per-server cards on the "Servers" page: each server (the master
// included) owns its own, edited from its card rather than as global tabs here.
const SUBTABS = [
  { value: "general", label: "settings.tabGeneral" },
  { value: "branding", label: "settings.tabBranding" },
  { value: "subscriptions", label: "settings.tabSubscriptions" },
  { value: "telegram", label: "settings.tabTelegram" },
  { value: "billing", label: "settings.tabBilling" },
  { value: "abuse", label: "settings.tabAbuse" },
  { value: "api", label: "settings.tabApi" },
] as const;

type SubTab = (typeof SUBTABS)[number]["value"];

// The admin roster deliberately lives outside this panel — it's the owner's own
// business, not a setting of the VPN — and hangs off the account menu instead.
// See AdminsSettings, rendered by Dashboard on the "admins" route.
export function SettingsPanel() {
  const { t: tr } = useTranslation();
  const seg = useRoute();
  const tab = (SUBTABS.find((t) => t.value === seg[1])?.value ??
    "general") as SubTab;
  return (
    // One section for the screen, the same shape the users page has: a header band, the
    // tab strip, and a content area that owns the scroll — so each section's own save
    // bar can stick to the bottom of the screen instead of floating over the page.
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-brand-600/10 bg-white">
      <div className="flex min-h-14 items-center gap-3 border-b border-brand-600/10 px-5 py-2">
        <h2 className="text-base font-semibold text-ink">{tr("nav.settings")}</h2>
      </div>

      <div className="no-scrollbar flex gap-0.5 overflow-x-auto border-b border-brand-600/10 px-5">
        {SUBTABS.map((t) => (
          <button
            key={t.value}
            onClick={() =>
              navigate(
                t.value === "general" ? "settings" : `settings/${t.value}`,
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
        {tab === "general" && <GeneralSettings />}
        {tab === "branding" && <BrandingSettings />}
        {tab === "subscriptions" && <SubscriptionsPanel />}
        {tab === "telegram" && <TelegramSettings />}
        {tab === "billing" && <BillingPanel />}
        {tab === "abuse" && <AbuseSettings />}
        {tab === "api" && (
          <>
            <ApiSettings />
            <WebhooksSettings />
          </>
        )}
      </div>
    </div>
  );
}
