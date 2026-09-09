import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AdminsSettings } from "./AdminsSettings";
import { getMe, logout } from "./api";
import { Credentials } from "./Credentials";
import { ChangelogModal } from "./ChangelogModal";
import { LangChoice, LangPills } from "./LangSwitch";
import { BrandLogo } from "./Logo";
import { OverviewPanel } from "./OverviewPanel";
import { useIsAdmin, useIsOwner } from "./role";
import { NodesPanel } from "./NodesPanel";
import { navigate, useRoute } from "./router";
import { SettingsPanel } from "./SettingsPanel";
import {
  cn,
  Dropdown,
  DropdownDivider,
  DropdownItem,
  DropdownLabel,
  IconChevron,
  IconDots,
  IconGear,
  IconPulse,
  IconServer,
  IconShield,
  IconUsers,
  MICRO,
  Mono,
  Panel,
} from "./ui";
import { UsersPage } from "./UsersPage";

// Statistics and the journal aren't tabs: they're sub-tabs of "users" (see
// UsersPage), because both only ever describe end users.
type Tab = "overview" | "users" | "nodes" | "settings" | "admins";

const SOURCE_URL = "https://github.com/AppsGanin/rospanel";

// How many destinations the phone's bottom bar carries before the "More" tab. The
// rest of the nav — and everything about the account — lives behind that tab, which
// is why there is no burger and no side drawer on a phone any more.
const BOTTOM_TABS = 3;

// The console frame: a 224px sidebar, a 52px topbar and a content area that owns
// the scroll. One breakpoint, at Tailwind's `sm` (640px): below it the sidebar
// moves into a left drawer behind a burger, the first four destinations repeat as
// a bottom bar, and the content padding drops from 20px to 12px. No screen inside
// branches on width. Colours are the operator's five branding inputs throughout —
// there is no second theme and nothing here names a colour literally.
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
  const { t } = useTranslation();
  const seg = useRoute();
  const isAdmin = useIsAdmin();
  const isOwner = useIsOwner();
  // The phone's "More" tab: the rest of the nav and the account, over the content.
  const [moreOpen, setMoreOpen] = useState(false);
  const [credsOpen, setCredsOpen] = useState(false);
  const [changelogOpen, setChangelogOpen] = useState(false);
  // Keep the payments-enabled flag fresh so the "Payments" item appears/vanishes
  // without a full reload: re-read on every top-level tab change AND whenever the
  // Both flags are saved in Settings, which fires an event when it does. Without a
  // refresh they would keep their login-time values until a full page reload: the
  // Broadcast tab and the per-user "send a message" button would stay hidden
  // after switching the user bot ON, and — worse — stay visible after switching it
  // OFF, so every action behind them would answer 400.
  const [billing, setBilling] = useState(billingEnabled);
  const [userBot, setUserBot] = useState(userBotEnabled);
  const refreshFlags = () =>
    getMe()
      .then((m) => {
        setBilling(!!m.billing_enabled);
        setUserBot(!!m.user_bot_enabled);
      })
      .catch(() => {});
  useEffect(() => {
    refreshFlags();
  }, [seg[0]]);
  useEffect(() => {
    const h = () => refreshFlags();
    window.addEventListener("rospanel:billing-changed", h);
    window.addEventListener("rospanel:telegram-changed", h);
    return () => {
      window.removeEventListener("rospanel:billing-changed", h);
      window.removeEventListener("rospanel:telegram-changed", h);
    };
  }, []);

  // An operator gets the sections whose routes they can actually call: the dashboard
  // and the users section (list, stats, journal). Settings and the servers page are
  // admin-and-up, the roster is the owner's alone — and a section someone cannot use
  // is not rendered at all rather than rendered disabled. If they navigate to
  // /settings by hand, `tab` falls back to the dashboard rather than showing a page
  // whose every request would 403.
  const NAV: { value: Tab; label: string; icon: ReactNode }[] = [
    { value: "overview", label: t("nav.overview"), icon: <IconPulse size={18} /> },
    { value: "users", label: t("nav.users"), icon: <IconUsers size={18} /> },
    ...(isAdmin
      ? [{ value: "nodes" as Tab, label: t("nav.servers"), icon: <IconServer size={18} /> }]
      : []),
    ...(isOwner
      ? [{ value: "admins" as Tab, label: t("nav.admins"), icon: <IconShield size={18} /> }]
      : []),
    ...(isAdmin
      ? [{ value: "settings" as Tab, label: t("nav.settings"), icon: <IconGear size={18} /> }]
      : []),
  ];
  const tab: Tab = (NAV.find((n) => n.value === seg[0])?.value ??
    "overview") as Tab;
  const title = NAV.find((n) => n.value === tab)?.label ?? "";

  const doLogout = async () => {
    setMoreOpen(false);
    try {
      await logout();
    } finally {
      onLogout();
    }
  };

  const go = (t: Tab) => {
    navigate(t === "overview" ? "" : t);
    setMoreOpen(false);
  };

  // The phone's "More" sheet in two blocks: what the panel is (documents, source),
  // then who you are signed in as — with the way out at the very bottom, where a
  // destructive action belongs.
  type MoreItem = { label: string; onClick: () => void; danger?: boolean };
  const docItems: MoreItem[] = [
    { label: t("nav.agreement"), onClick: onShowAgreement },
    { label: t("nav.donate"), onClick: onShowDonate },
    { label: t("nav.changelog"), onClick: () => setChangelogOpen(true) },
    { label: t("nav.sourceOnGithub"), onClick: () => window.open(SOURCE_URL, "_blank") },
  ];
  const accountItems: MoreItem[] = [
    { label: t("nav.credentials"), onClick: () => setCredsOpen(true) },
  ];

  // The account menu: everything about the person signed in, plus the two document
  // links and the source link that used to sit in a page footer the console frame
  // no longer has.
  const accountMenu = (
    <>
      <DropdownLabel>{username}</DropdownLabel>
      <DropdownDivider />
      <DropdownItem onClick={() => setCredsOpen(true)}>
        {t("nav.credentials")}
      </DropdownItem>
      <DropdownDivider />
      <DropdownLabel>{t("common.language")}</DropdownLabel>
      <LangChoice />
      <DropdownDivider />
      <DropdownItem onClick={onShowAgreement}>{t("nav.agreement")}</DropdownItem>
      <DropdownItem onClick={onShowDonate}>{t("nav.donate")}</DropdownItem>
      <DropdownItem onClick={() => setChangelogOpen(true)}>
        {t("nav.changelog")}
      </DropdownItem>
      <DropdownItem href={SOURCE_URL} target="_blank">
        {t("nav.sourceOnGithub")}
      </DropdownItem>
      <DropdownDivider />
      <DropdownItem color="red" onClick={doLogout}>
        {t("nav.logout")}
      </DropdownItem>
    </>
  );

  return (
    // The shell owns the viewport height so the content area — and only it —
    // scrolls. body carries the safe-area insets (index.css), so they come off
    // the available height here rather than being padded a second time.
    <div className="flex h-[calc(100dvh-env(safe-area-inset-top)-env(safe-area-inset-bottom))] overflow-hidden">
      <aside className="hidden w-56 shrink-0 flex-col border-r border-brand-600/6 bg-gray-50 sm:flex">
        <div className="flex h-13 shrink-0 items-center gap-2.5 border-b border-brand-600/6 px-4">
          <BrandLogo size={22} />
          {version && (
            <Mono className="ml-auto shrink-0 text-[11px] text-ink-muted">
              {version}
            </Mono>
          )}
        </div>

        <nav className="flex flex-col gap-0.5 p-2 pt-3">
          {NAV.map((n) => (
            <NavItem
              key={n.value}
              label={n.label}
              active={tab === n.value}
              onClick={() => go(n.value)}
            />
          ))}
        </nav>

        {/* Only the account lives in the footer. The domain, the certificate and the
            Xray version used to be chrome; they belong to the Overview and to the
            server's Domain tab, where they can be acted on. */}
        <div className="mt-auto border-t border-brand-600/6 p-3">
          <Dropdown
            up
            align="start"
            width={224}
            trigger={
              <span
                title={t("nav.account")}
                className="flex items-center gap-2 rounded-lg px-1.5 py-1.5 transition hover:bg-gray-100"
              >
                <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-gray-200 text-[11px] font-semibold text-gray-800">
                  {username.slice(0, 1).toUpperCase()}
                </span>
                <span className="min-w-0 truncate text-xs text-gray-800">
                  {username}
                </span>
                <IconChevron className="ml-auto shrink-0 text-gray-400" />
              </span>
            }
          >
            {accountMenu}
          </Dropdown>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-13 shrink-0 items-center gap-4 border-b border-brand-600/6 px-3 sm:px-5">
          <h1 className="min-w-0 truncate text-base font-semibold text-ink">
            {moreOpen ? t("nav.more") : title}
          </h1>
          <Clock />
        </header>

        {/* relative: the "More" sheet covers this area and nothing else — the header
            above it and the tab bar below it stay where they are. */}
        <div className="relative flex min-h-0 flex-1 flex-col">
        <main className="min-w-0 flex-1 overflow-y-auto p-3 sm:p-5">
          {/* h-full, not min-h-full: a definite height is what lets a screen that
              owns its scroll (users, settings) keep its header band and its save bar
              in place while the middle scrolls. Screens taller than that overflow
              visibly and main scrolls them as before. */}
          <div key={tab} className="flex h-full flex-col animate-fade-in">
            {tab === "overview" && <OverviewPanel />}
            {tab === "users" && (
              <UsersPage userBotEnabled={userBot} billingEnabled={billing} />
            )}
            {tab === "nodes" && <NodesPanel />}
            {tab === "settings" && <SettingsPanel />}
            {tab === "admins" && <AdminsSettings />}
          </div>
        </main>

        {moreOpen && (
          <div className="absolute inset-0 z-30 overflow-y-auto bg-gray-50 p-3 sm:hidden">
            <div className="flex flex-col gap-3.5">
              {/* Whatever did not fit in the bar: the rest of the destinations… */}
              {NAV.length > BOTTOM_TABS && (
                <Panel>
                  {NAV.slice(BOTTOM_TABS).map((n) => (
                    <button
                      key={n.value}
                      onClick={() => go(n.value)}
                      className="flex w-full items-center gap-2.5 border-t border-gray-100 px-3.5 py-2.5 text-left text-[13px] font-medium text-ink first:border-t-0"
                    >
                      <span
                        className={cn(
                          "size-1.5 shrink-0 rounded-full",
                          tab === n.value ? "bg-brand-600" : "bg-gray-400",
                        )}
                      />
                      {n.label}
                    </button>
                  ))}
                </Panel>
              )}

              {/* What the panel is: the documents and where its source lives. */}
              <Panel>
                {docItems.map((it) => (
                  <MoreRow key={it.label} item={it} onDone={() => setMoreOpen(false)} />
                ))}
              </Panel>

              {/* …and who is signed in, which the desktop keeps in the sidebar
                  footer. The way out is the last row of the last block. */}
              <Panel title={username}>
                {accountItems.map((it) => (
                  <MoreRow key={it.label} item={it} onDone={() => setMoreOpen(false)} />
                ))}
                <div className="flex items-center justify-between gap-3 border-t border-gray-100 px-3.5 py-2.5">
                  <span className={MICRO}>{t("common.language")}</span>
                  <LangPills />
                </div>
                <MoreRow
                  item={{ label: t("nav.logout"), onClick: doLogout, danger: true }}
                  onDone={() => setMoreOpen(false)}
                />
              </Panel>
            </div>
          </div>
        )}
        </div>

        {/* The three most-used destinations, repeated where a thumb reaches them.
            Named rather than "the first four": the roster sits above Settings in the
            sidebar, and a thumb reaching for Settings must not find it missing.
            Rows are 44px tall: the whole strip is a touch target, not a dense one. */}
        <nav className="relative z-40 flex shrink-0 border-t border-brand-600/6 bg-gray-50 px-1 py-1.5 sm:hidden">
          {NAV.slice(0, BOTTOM_TABS).map((n) => {
            const active = !moreOpen && tab === n.value;
            return (
              <button
                key={n.value}
                onClick={() => {
                  setMoreOpen(false);
                  go(n.value);
                }}
                className={cn(
                  "flex flex-1 flex-col items-center justify-center gap-1 rounded-lg text-xs font-semibold transition",
                  active ? "text-accent" : "text-ink-muted",
                )}
              >
                {n.icon}
                <span className="max-w-full truncate px-1">{n.label}</span>
              </button>
            );
          })}
          <button
            onClick={() => setMoreOpen((o) => !o)}
            className={cn(
              "flex flex-1 flex-col items-center justify-center gap-1 rounded-lg text-xs font-semibold transition",
              moreOpen ? "text-accent" : "text-ink-muted",
            )}
          >
            <IconDots size={18} />
            <span className="max-w-full truncate px-1">{t("nav.more")}</span>
          </button>
        </nav>
      </div>


      {changelogOpen && <ChangelogModal onClose={() => setChangelogOpen(false)} />}
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

// A sidebar destination: a 6px state dot, the label, nothing else. The dot is what
// carries "you are here" in the console theme, where the active row is a tint
// rather than a border.
function NavItem({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm font-medium transition",
        active ? "accent-tint text-ink" : "text-ink-muted hover:bg-gray-100",
      )}
    >
      <span
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          active ? "bg-brand-600" : "bg-gray-400",
        )}
      />
      <span className="truncate">{label}</span>
    </button>
  );
}

// MoreRow is one line of the phone's "More" sheet: a full-width target, the action
// in accent, the way out in danger.
function MoreRow({
  item,
  onDone,
}: {
  item: { label: string; onClick: () => void; danger?: boolean };
  onDone: () => void;
}) {
  return (
    <button
      onClick={() => {
        onDone();
        item.onClick();
      }}
      className={cn(
        "flex w-full items-center border-t border-gray-100 px-3.5 py-2.5 text-left text-[13px] font-medium first:border-t-0",
        item.danger ? "text-danger" : "text-accent",
      )}
    >
      {item.label}
    </button>
  );
}

// Clock is the topbar's right edge: the reader's own wall time, ticking. Schedules,
// expiry dates and backup windows are all read in this timezone, so the panel says
// out loud which one it is showing them in.
function Clock() {
  const { t, i18n } = useTranslation();
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    // Line the tick up with the next whole minute instead of drifting a second
    // later on every render.
    let timer: number;
    const schedule = () => {
      timer = window.setTimeout(() => {
        setNow(new Date());
        schedule();
      }, 60000 - (Date.now() % 60000));
    };
    schedule();
    return () => window.clearTimeout(timer);
  }, []);

  const text = new Intl.DateTimeFormat(i18n.language, {
    hour: "2-digit",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(now);

  return (
    <Mono
      title={t("common.localTime")}
      className="ml-auto shrink-0 text-xs text-ink-muted"
    >
      {text}
    </Mono>
  );
}
