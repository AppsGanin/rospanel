import { useEffect, useState } from "react";
import { AdminsSettings } from "./AdminsSettings";
import { getMe, logout } from "./api";
import { Credentials } from "./Credentials";
import { BrandLogo } from "./Logo";
import { OverviewPanel } from "./OverviewPanel";
import { PaymentsPage } from "./PaymentsPage";
import { useIsAdmin, useIsOwner } from "./role";
import { NodesPanel } from "./NodesPanel";
import { navigate, useRoute } from "./router";
import { SettingsPanel } from "./SettingsPanel";
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
import { UsersPage } from "./UsersPage";

// "admins" is a page without a nav tab: the roster is reached from the account menu
// (it's about who runs the panel, not about how the VPN is configured), so it never
// appears in NAV — only in the route.
// Statistics and the journal aren't tabs either: they're sub-tabs of "users"
// (see UsersPage), because both only ever describe end users.
type Tab = "overview" | "users" | "nodes" | "payments" | "settings" | "admins";

export function Dashboard({
  username,
  version,
  billingEnabled,
  userBotEnabled,
  onLogout,
  onShowAgreement,
  onShowDonate,
  onAccountChanged,
}: {
  username: string;
  version: string;
  billingEnabled: boolean;
  userBotEnabled: boolean;
  onLogout: () => void;
  onShowAgreement: () => void;
  onShowDonate: () => void;
  onAccountChanged: () => void;
}) {
  const seg = useRoute();
  const isAdmin = useIsAdmin();
  const isOwner = useIsOwner();
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

  // An operator gets the tabs whose routes they can actually call: the dashboard and
  // the users section (list, stats, journal). Settings and the payments desk are
  // admin-and-up, so they're not rendered — and if an operator navigates to /settings
  // by hand, `tab` falls back to the dashboard rather than showing a page whose every
  // request would 403.
  const NAV: { value: Tab; label: string }[] = [
    { value: "overview", label: "Дашборд" },
    { value: "users", label: "Пользователи" },
    ...(isAdmin ? [{ value: "nodes" as Tab, label: "Сервера" }] : []),
    ...(billing && isAdmin ? [{ value: "payments" as Tab, label: "Оплата" }] : []),
    ...(isAdmin ? [{ value: "settings" as Tab, label: "Настройки" }] : []),
  ];
  // The roster isn't in NAV, so resolve it separately — and only for the owner, so
  // hand-typing /admins as anyone else lands on the dashboard rather than on a page
  // whose every request would 403.
  const onAdmins = seg[0] === "admins" && isOwner;
  const tab: Tab = onAdmins
    ? "admins"
    : ((NAV.find((n) => n.value === seg[0])?.value ?? "overview") as Tab);

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

  const goAdmins = () => {
    navigate("admins");
    setMenuOpen(false);
  };

  return (
    <div className="flex min-h-dvh flex-col">
      {/* White sticky top bar. */}
      <header className="sticky top-0 z-100 border-b border-brand-600/10 bg-white shadow-sm">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-3 px-3 sm:px-4">
          {/* min-w-0 lets this box shrink so the row never overflows the viewport;
              BrandLogo ellipses instead of spilling over the badge next to it. The
              badge is decorative, so it's the first thing to go when space is tight —
              below lg the nav needs every pixel, and hiding it keeps the panel name
              whole instead of truncating it to make room. */}
          <div className="flex min-w-0 gap-2 items-center">
            <button
              className="text-gray-600 md:hidden"
              onClick={() => setMenuOpen(true)}
              aria-label="Меню"
            >
              <IconBurger />
            </button>
            <BrandLogo size={26} />
            {version && (
              <span className="hidden shrink-0 whitespace-nowrap rounded-full self-start accent-tint px-2 py-0.5 text-xs font-medium text-ink-muted lg:inline">
                v{version}
              </span>
            )}
          </div>

          {/* Desktop nav + account. min-w-0 is what lets the nav actually shrink —
              a flex item's default min-width:auto floors it at its content width, so
              overflow-x-auto on the nav would never engage without it. */}
          <div className="hidden min-w-0 items-center gap-8 md:flex">
            <nav className="no-scrollbar flex min-w-0 items-center gap-7 overflow-x-auto">
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
              {isOwner && (
                <DropdownItem onClick={goAdmins}>Администраторы</DropdownItem>
              )}
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
          {isOwner && (
            <button
              onClick={goAdmins}
              className={cn(
                "py-2 text-left text-lg font-medium transition",
                onAdmins ? "text-brand-800" : "text-accent",
              )}
            >
              Администраторы
            </button>
          )}
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
          {tab === "users" && <UsersPage userBotEnabled={userBotEnabled} />}
          {tab === "nodes" && <NodesPanel />}
          {tab === "payments" && <PaymentsPage />}
          {tab === "settings" && <SettingsPanel />}
          {tab === "admins" && <AdminsSettings />}
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
