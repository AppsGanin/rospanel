import { useEffect, useState } from "react";
import { getMe, logout } from "./api";
import { Credentials } from "./Credentials";
import { BrandLogo } from "./Logo";
import { EventsPanel } from "./EventsPanel";
import { OverviewPanel } from "./OverviewPanel";
import { PaymentsPage } from "./PaymentsPage";
import { navigate, useRoute } from "./router";
import { SettingsPanel } from "./SettingsPanel";
import { StatsPanel } from "./StatsPanel";
import {
  cn,
  Drawer,
  Dropdown,
  DropdownDivider,
  DropdownItem,
  DropdownLabel,
  IconBurger,
  IconChevron,
  IconGithub,
} from "./ui";
import { UsersPanel } from "./UsersPanel";

type Tab = "overview" | "users" | "stats" | "payments" | "events" | "settings";

export function Dashboard({
  username,
  version,
  billingEnabled,
  onLogout,
  onShowAgreement,
  onShowDonate,
  onAccountChanged,
}: {
  username: string;
  version: string;
  billingEnabled: boolean;
  onLogout: () => void;
  onShowAgreement: () => void;
  onShowDonate: () => void;
  onAccountChanged: () => void;
}) {
  const seg = useRoute();
  const [menuOpen, setMenuOpen] = useState(false);
  const [credsOpen, setCredsOpen] = useState(false);
  // Keep the payments-enabled flag fresh so the "Оплата" item appears/vanishes
  // without a full reload: re-read on every top-level tab change AND whenever the
  // billing toggle is saved in Settings (which fires "rospanel:billing-changed").
  const [billing, setBilling] = useState(billingEnabled);
  const refreshBilling = () =>
    getMe()
      .then((m) => setBilling(!!m.billing_enabled))
      .catch(() => {});
  useEffect(() => {
    refreshBilling();
  }, [seg[0]]);
  useEffect(() => {
    const h = () => refreshBilling();
    window.addEventListener("rospanel:billing-changed", h);
    return () => window.removeEventListener("rospanel:billing-changed", h);
  }, []);

  const NAV: { value: Tab; label: string }[] = [
    { value: "overview", label: "Дашборд" },
    { value: "users", label: "Пользователи" },
    { value: "stats", label: "Статистика" },
    ...(billing ? [{ value: "payments" as Tab, label: "Оплата" }] : []),
    { value: "events", label: "Журнал" },
    { value: "settings", label: "Настройки" },
  ];
  const tab = (NAV.find((n) => n.value === seg[0])?.value ?? "overview") as Tab;

  const doLogout = async () => {
    setMenuOpen(false);
    try {
      await logout();
    } finally {
      onLogout();
    }
  };

  const go = (t: Tab) => {
    navigate(t === "overview" ? "" : t);
    setMenuOpen(false);
  };

  return (
    <div className="flex min-h-dvh flex-col">
      {/* White sticky top bar. */}
      <header className="sticky top-0 z-100 border-b border-brand-600/10 bg-white shadow-sm">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-3 px-3 sm:px-4">
          <div className="flex gap-2 items-center">
            <button
              className="text-gray-600 md:hidden"
              onClick={() => setMenuOpen(true)}
              aria-label="Меню"
            >
              <IconBurger />
            </button>
            <BrandLogo size={26} />
            {version && (
              <span className="rounded-full self-start accent-tint px-2 py-0.5 text-xs font-medium text-ink-muted sm:inline">
                v{version}
              </span>
            )}
          </div>

          {/* Desktop nav + account. */}
          <div className="hidden items-center gap-8 md:flex">
            <nav className="no-scrollbar flex items-center gap-7 overflow-x-auto">
              {NAV.map((n) => (
                <button
                  key={n.value}
                  onClick={() => go(n.value)}
                  className={cn(
                    "whitespace-nowrap py-1 text-sm font-medium transition",
                    tab === n.value
                      ? " text-brand-800"
                      : " text-accent hover:text-brand-800",
                  )}
                >
                  {n.label}
                </button>
              ))}
            </nav>

            <Dropdown
              trigger={
                <span className="flex items-center gap-1.5 rounded-full px-3 py-1.5 transition hover:bg-gray-100">
                  <span className="max-w-35 truncate text-sm font-medium text-ink-muted">
                    {username}
                  </span>
                  <IconChevron className="text-gray-400" />
                </span>
              }
            >
              <DropdownLabel>{username}</DropdownLabel>
              <DropdownDivider />
              <DropdownItem onClick={() => setCredsOpen(true)}>
                Учётные данные
              </DropdownItem>
              <DropdownItem color="red" onClick={doLogout}>
                Выйти
              </DropdownItem>
            </Dropdown>
          </div>
        </div>
      </header>

      {/* Mobile full-screen menu. */}
      <Drawer
        open={menuOpen}
        onClose={() => setMenuOpen(false)}
        side="left"
        full
        title={<BrandLogo size={24} />}
      >
        <p className="mb-2 text-lg font-semibold text-ink">{username}</p>
        <nav className="flex flex-col">
          {NAV.map((n) => (
            <button
              key={n.value}
              onClick={() => go(n.value)}
              className={cn(
                "py-2 text-left text-lg font-medium transition",
                tab === n.value ? "text-brand-800" : "text-accent",
              )}
            >
              {n.label}
            </button>
          ))}
        </nav>
        <hr className="my-4 border-gray-200" />
        <button
          onClick={doLogout}
          className="text-lg font-medium text-danger"
        >
          Выйти
        </button>
      </Drawer>

      <main className="mx-auto w-full max-w-6xl flex-1 px-3 py-6 sm:px-4">
        <div key={tab} className="animate-fade-in">
          {tab === "overview" && <OverviewPanel />}
          {tab === "users" && <UsersPanel />}
          {tab === "stats" && <StatsPanel />}
          {tab === "payments" && <PaymentsPage />}
          {tab === "events" && <EventsPanel />}
          {tab === "settings" && <SettingsPanel />}
        </div>
      </main>

      <footer className="mx-auto grid w-full max-w-6xl grid-cols-[1fr_auto_1fr] items-center gap-2 px-3 pb-8 pt-2 text-xs text-ink-muted sm:px-4">
        {/* empty left cell balances the right-hand icon so the links stay centered */}
        <span aria-hidden />
        <div className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-center">
          <button
            onClick={onShowAgreement}
            className="transition hover:text-accent"
          >
            Пользовательское соглашение
          </button>
          <button
            onClick={onShowDonate}
            className="transition hover:text-accent"
          >
            Пожертвования
          </button>
        </div>
        <a
          href="https://github.com/AppsGanin/rospanel"
          target="_blank"
          rel="noreferrer"
          aria-label="GitHub"
          title="Исходный код на GitHub"
          className="justify-self-end text-gray-400 transition hover:text-accent"
        >
          <IconGithub size={18} />
        </a>
      </footer>

      {credsOpen && (
        <Credentials
          username={username}
          onUpdated={onAccountChanged}
          onClose={() => setCredsOpen(false)}
        />
      )}
    </div>
  );
}
