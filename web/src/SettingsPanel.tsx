import { ApiSettings } from "./ApiSettings";
import { BillingPanel } from "./BillingPanel";
import { BrandingSettings } from "./BrandingSettings";
import { ConnectionsPanel } from "./ConnectionsPanel";
import { GeneralSettings } from "./GeneralSettings";
import { navigate, useRoute } from "./router";
import { SubscriptionsPanel } from "./SubscriptionsPanel";
import { TelegramSettings } from "./TelegramSettings";
import { cn } from "./ui";
import { WebhooksSettings } from "./WebhooksSettings";

// The server's domain, routing, DNS and site decoy moved to the per-server settings
// on the "Сервера" page: each server (the master included) owns its own address and
// routing/DNS/egress, edited from its server card rather than as global tabs here.
const SUBTABS = [
  { value: "general", label: "Основное" },
  { value: "branding", label: "Брендинг" },
  { value: "connections", label: "Подключения" },
  { value: "subscriptions", label: "Подписки" },
  { value: "telegram", label: "Telegram" },
  { value: "billing", label: "Оплата" },
  { value: "api", label: "API" },
] as const;

type SubTab = (typeof SUBTABS)[number]["value"];

// The admin roster deliberately lives outside this panel — it's the owner's own
// business, not a setting of the VPN — and hangs off the account menu instead.
// See AdminsSettings, rendered by Dashboard on the "admins" route.
export function SettingsPanel() {
  const seg = useRoute();
  const tab = (SUBTABS.find((t) => t.value === seg[1])?.value ??
    "general") as SubTab;
  return (
    <div className="flex flex-col gap-4">
      <div className="no-scrollbar flex gap-1 overflow-x-auto border-b border-gray-200">
        {SUBTABS.map((t) => (
          <button
            key={t.value}
            onClick={() =>
              navigate(
                t.value === "general" ? "settings" : `settings/${t.value}`,
              )
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
        {tab === "general" && <GeneralSettings />}
        {tab === "branding" && <BrandingSettings />}
        {tab === "connections" && <ConnectionsPanel />}
        {tab === "subscriptions" && <SubscriptionsPanel />}
        {tab === "telegram" && <TelegramSettings />}
        {tab === "billing" && <BillingPanel />}
        {tab === "api" && (
          <div className="flex flex-col gap-4">
            <ApiSettings />
            <WebhooksSettings />
          </div>
        )}
      </div>
    </div>
  );
}
