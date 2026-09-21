import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import { type SystemProxy, type SystemProxyAccount } from "./api";
import { Button, Code, IconButton, IconTrash, Section, SettingRow, Switch, TextInput } from "./ui";

// systemProxyIssue names why a proxy draft cannot be saved, or "" when it can. The
// same rules the server enforces (model.SystemProxy.Validate) — checked here too so
// the operator sees the reason next to the field instead of a toast after a round
// trip, and so Save is not offered for a state that will bounce.
export function systemProxyIssue(p: SystemProxy): string {
  if (!p.socks_enabled && !p.http_enabled) return "";
  const accounts = p.accounts ?? [];
  if (accounts.length === 0) return i18n.t("err.proxyNeedsAccount");
  const seen = new Set<string>();
  for (const a of accounts) {
    const user = a.user.trim();
    if (!user) return i18n.t("err.proxyAccountNoUser");
    if (!a.pass.trim()) return i18n.t("err.proxyAccountNoPass", { value: user });
    if (/[: ]/.test(user)) return i18n.t("err.proxyUserCharset");
    if (seen.has(user)) return i18n.t("err.proxyUserDuplicate", { value: user });
    seen.add(user);
  }
  if (p.socks_enabled && p.http_enabled && p.socks_port === p.http_port) {
    return i18n.t("err.proxyPortsCollide");
  }
  return "";
}

// randomProxyPass mints a password for a first-time enable, so the operator never
// has to invent one (and never leaves the field to whatever they type twice).
function randomProxyPass(): string {
  const bytes = new Uint8Array(18);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes))
    .replace(/[+/=]/g, "")
    .slice(0, 20);
}

// SystemProxyEditor is one server's SOCKS/HTTP forward proxy — the same panel for
// the master and for a node, because the listener is the same thing on both. These
// proxies are NOT part of the VPN surface: no user's credential opens them, no access
// group gates them, they never appear in a subscription. They exist so something that
// isn't a VPN client can go out through this server.
//
// Rendered INSIDE the General tab's server card, not as a card of its own: it is one
// switch and a port per protocol, and a separate panel with a separate save button
// read as a second, unrelated screen. The draft and the last-saved copy belong to the
// tab, so the whole tab still has exactly one save.
export function SystemProxyEditor({
  host,
  value,
  saved,
  onChange,
}: {
  host: string;
  value: SystemProxy;
  saved: SystemProxy; // last-saved copy: what the ready-to-paste addresses describe
  onChange: (p: SystemProxy) => void;
}) {
  const { t } = useTranslation();
  const cur = value;
  const base = saved;
  const patch = (p: Partial<SystemProxy>) => onChange({ ...cur, ...p });
  const on = cur.socks_enabled || cur.http_enabled;

  const accounts = cur.accounts ?? [];

  // Turning a protocol on for the first time fills in the port and mints an account:
  // an enable that then refuses to save because there is nobody to authenticate is a
  // worse first experience than one that just works and can be edited.
  const enable = (key: "socks_enabled" | "http_enabled", v: boolean) => {
    const next: Partial<SystemProxy> = { [key]: v } as Partial<SystemProxy>;
    if (v) {
      if (key === "socks_enabled" && !cur.socks_port) next.socks_port = 1080;
      if (key === "http_enabled" && !cur.http_port) next.http_port = 3128;
      if (accounts.length === 0) next.accounts = [{ user: "proxy", pass: randomProxyPass() }];
    }
    patch(next);
  };

  const setAccount = (i: number, a: Partial<SystemProxyAccount>) =>
    patch({ accounts: accounts.map((old, j) => (j === i ? { ...old, ...a } : old)) });
  // The suggested login is the first free proxyN, not "count + 1": deleting the
  // first row and adding one would otherwise propose a name already in the list, and
  // the save would come back with "the login is used twice".
  const addAccount = () => {
    const taken = new Set(accounts.map((a) => a.user));
    let n = accounts.length + 1;
    while (taken.has(`proxy${n}`)) n++;
    patch({ accounts: [...accounts, { user: `proxy${n}`, pass: randomProxyPass() }] });
  };
  const removeAccount = (i: number) =>
    patch({ accounts: accounts.filter((_, j) => j !== i) });

  // savedAccount is the stored twin of a draft row, matched on the exact credentials:
  // an address is only truthful for a login the server has actually been given, so a
  // row that is still being typed shows none.
  const savedAccount = (i: number): SystemProxyAccount | undefined =>
    (base.accounts ?? []).find(
      (a) => a.user === accounts[i]?.user && a.pass === accounts[i]?.pass,
    );
  const url = (scheme: string, enabled: boolean, port: number, a: SystemProxyAccount) =>
    enabled && host && port
      ? `${scheme}://${encodeURIComponent(a.user)}:${encodeURIComponent(a.pass)}@${host}:${port}`
      : "";

  return (
    <Section
      title={t("proxy.title")}
      desc={t("proxy.hint")}
      action={
        on ? (
          <Button size="xs" variant="light" onClick={addAccount}>
            {t("proxy.addAccount")}
          </Button>
        ) : undefined
      }
      flush
    >
      <ProxyListenerRow
        label="SOCKS5"
        enabled={cur.socks_enabled}
        port={cur.socks_port}
        defaultPort={1080}
        onToggle={(v) => enable("socks_enabled", v)}
        onPort={(v) => patch({ socks_port: v })}
      />
      <ProxyListenerRow
        label="HTTP"
        enabled={cur.http_enabled}
        port={cur.http_port}
        defaultPort={3128}
        onToggle={(v) => enable("http_enabled", v)}
        onPort={(v) => patch({ http_port: v })}
      />

      {/* The accounts appear once something is listening: with both protocols off
          there is nobody to authenticate, and empty rows would just be noise. Each
          account is its own row so one consumer can be revoked without touching the
          others — which is the whole reason there is a list rather than one login. */}
      {on && accounts.length === 0 && (
        <SettingRow hint={t("proxy.noAccounts")} />
      )}
      {on &&
        accounts.map((a, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: the account list is the wire payload — it carries no id, and its position is what every mutator here addresses
          <SettingRow key={i}>
            <div className="flex flex-col gap-2">
              {/* Login, password and the delete control on ONE line: the button
                  belongs to this account, and on its own row it read as an action on
                  the whole list. */}
              <div className="flex items-end gap-2">
                <div className="min-w-0 flex-1">
                  <TextInput
                    label={t("proxy.user")}
                    value={a.user}
                    onChange={(v) => setAccount(i, { user: v })}
                  />
                </div>
                <div className="min-w-0 flex-1">
                  <TextInput
                    label={t("proxy.pass")}
                    mono
                    value={a.pass}
                    onChange={(v) => setAccount(i, { pass: v })}
                  />
                </div>
                <IconButton
                  color="red"
                  title={t("common.delete")}
                  onClick={() => removeAccount(i)}
                >
                  <IconTrash />
                </IconButton>
              </div>
              {/* The addresses come from the SAVED copy: a URL built from a port that
                  is still only typed into the form points at nothing. */}
              {savedAccount(i) && (
                <div className="flex flex-col gap-1.5">
                  {url("socks5", base.socks_enabled, base.socks_port, savedAccount(i)!) && (
                    <Code block copy>
                      {url("socks5", base.socks_enabled, base.socks_port, savedAccount(i)!)}
                    </Code>
                  )}
                  {url("http", base.http_enabled, base.http_port, savedAccount(i)!) && (
                    <Code block copy>
                      {url("http", base.http_enabled, base.http_port, savedAccount(i)!)}
                    </Code>
                  )}
                </div>
              )}
            </div>
          </SettingRow>
        ))}
    </Section>
  );
}

// ProxyListenerRow is one protocol of the system proxy. The port input carries no
// label of its own — a floating "Port" caption above a switch row is what made this
// block look like a pile of unrelated fields — and it is width-boxed by a wrapper
// because the shared input is w-full by design.
function ProxyListenerRow({
  label,
  enabled,
  port,
  defaultPort,
  onToggle,
  onPort,
}: {
  label: string;
  enabled: boolean;
  port: number;
  defaultPort: number;
  onToggle: (v: boolean) => void;
  onPort: (v: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <SettingRow
      label={label}
      hint={!enabled ? t("conn.off") : undefined}
      control={
        <div className="flex items-center gap-3">
          <span className="text-[11px] text-ink-muted">{t("conn.port")}</span>
          <div className="w-24">
            {/* A listener that was never given a port shows the one it will get when
                switched on, as a value rather than a placeholder: next to a row whose
                port was saved, a grey hint reads as a different kind of number. */}
            <TextInput
              type="number"
              value={port ? String(port) : enabled ? "" : String(defaultPort)}
              onChange={(v) => onPort(Number(v) || 0)}
              placeholder={String(defaultPort)}
              disabled={!enabled}
            />
          </div>
          <Switch checked={enabled} onChange={onToggle} />
        </div>
      }
    />
  );
}
