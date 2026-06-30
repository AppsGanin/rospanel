import { BillingPanel } from "./BillingPanel";
import { BrandingSettings } from "./BrandingSettings";
import { ConnectionsPanel } from "./ConnectionsPanel";
import { DnsSettings } from "./DnsSettings";
import { GeneralSettings } from "./GeneralSettings";
import { navigate, useRoute } from "./router";
import { RoutingPanel } from "./RoutingPanel";
import { SubscriptionsPanel } from "./SubscriptionsPanel";
import { TelegramSettings } from "./TelegramSettings";
import { TLSPanel } from "./TLSPanel";
import { cn } from "./ui";

const SUBTABS = [
  { value: "general", label: "Основное" },
  { value: "branding", label: "Брендинг" },
  { value: "connections", label: "Подключения" },
  { value: "subscriptions", label: "Подписки" },
  { value: "routing", label: "Роутинг" },
  { value: "dns", label: "DNS" },
  { value: "telegram", label: "Telegram" },
  { value: "billing", label: "Оплата" },
  { value: "domain", label: "Домен" },
] as const;

type SubTab = (typeof SUBTABS)[number]["value"];

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
        {tab === "routing" && <RoutingPanel />}
        {tab === "dns" && <DnsSettings />}
        {tab === "telegram" && <TelegramSettings />}
        {tab === "billing" && <BillingPanel />}
        {tab === "domain" && <TLSPanel />}
      </div>
    </div>
  );
}
