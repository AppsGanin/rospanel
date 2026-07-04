import { useEffect, useState } from "react";
import { getSettings, setXrayDNS } from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useDirtyForm } from "./hooks";
import { notifyError, notifySuccess } from "./notify";
import { Card, CenterLoader, Checkbox, SaveBar } from "./ui";

// sel holds the KEYS of the ticked presets (not the server strings), so one tick
// can contribute a primary+secondary pair.
type DnsState = { sel: string[]; custom: string };

// Popular Xray DNS resolvers offered as checkboxes. Each preset contributes its
// servers IN ORDER — primary first, then the secondary/fallback — so ticking one
// gives redundancy in a single click. Xray queries them in the listed order.
type DnsPreset = { key: string; label: string; servers: string[] };
const POPULAR_DNS: DnsPreset[] = [
  { key: "cloudflare", label: "Cloudflare — 1.1.1.1 / 1.0.0.1", servers: ["1.1.1.1", "1.0.0.1"] },
  { key: "cloudflare-doh", label: "Cloudflare DoH", servers: ["https://cloudflare-dns.com/dns-query"] },
  { key: "google", label: "Google — 8.8.8.8 / 8.8.4.4", servers: ["8.8.8.8", "8.8.4.4"] },
  { key: "google-doh", label: "Google DoH", servers: ["https://dns.google/dns-query"] },
  { key: "quad9", label: "Quad9 — 9.9.9.9 / 149.112.112.112", servers: ["9.9.9.9", "149.112.112.112"] },
  { key: "adguard", label: "AdGuard — 94.140.14.14 / 94.140.15.15", servers: ["94.140.14.14", "94.140.15.15"] },
  { key: "yandex", label: "Яндекс — 77.88.8.8 / 77.88.8.1", servers: ["77.88.8.8", "77.88.8.1"] },
  { key: "xbox-doh", label: "Xbox DNS — DoH", servers: ["https://xbox-dns.ru/dns-query"] },
  { key: "xbox", label: "Xbox DNS — 111.88.96.50 / 111.88.96.51", servers: ["111.88.96.50", "111.88.96.51"] },
  { key: "geohide-doh", label: "GeoHide — DoH", servers: ["https://dns.geohide.ru:444/dns-query"] },
  { key: "geohide", label: "GeoHide — 45.155.204.190 / 37.230.192.51", servers: ["45.155.204.190", "37.230.192.51"] },
];

// serversForKeys flattens the selected presets' servers in preset order, deduping
// (a server can't appear twice in the Xray DNS list).
function serversForKeys(keys: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const p of POPULAR_DNS) {
    if (!keys.includes(p.key)) continue;
    for (const s of p.servers) {
      if (!seen.has(s)) {
        seen.add(s);
        out.push(s);
      }
    }
  }
  return out;
}

// isValidDns mirrors the backend check: "localhost", a scheme URL, a bare IP, or
// IP:port.
function isValidDns(s: string): boolean {
  s = s.trim();
  if (s === "") return false;
  if (s === "localhost") return true;
  if (s.includes("://")) {
    try {
      return !!new URL(s).host;
    } catch {
      return false;
    }
  }
  const ipv4 = /^(\d{1,3}\.){3}\d{1,3}(:\d+)?$/;
  if (ipv4.test(s))
    return s
      .split(/[.:]/)
      .slice(0, 4)
      .every((o) => +o <= 255);
  if (/^\[[0-9a-fA-F:]+\]:\d+$/.test(s)) return true; // [IPv6]:port
  if (/^[0-9a-fA-F:]+$/.test(s) && s.includes(":")) return true; // bare IPv6
  return false;
}

export function DnsSettings() {
  const [loaded, setLoaded] = useState(false);
  const { applying, apply } = useXrayApply();
  const { draft, setDraft, saved, load, commit, reset } = useDirtyForm<DnsState>({ sel: [], custom: "" });
  const { sel, custom } = draft;

  useEffect(() => {
    getSettings()
      .then((s) => {
        const items = (s.xray_dns || "")
          .split(/[\n,]/)
          .map((x) => x.trim())
          .filter(Boolean);
        const itemSet = new Set(items);
        // A preset is selected when ALL of its servers are present.
        const initSel = POPULAR_DNS.filter((p) =>
          p.servers.every((srv) => itemSet.has(srv)),
        ).map((p) => p.key);
        // Anything not claimed by a selected preset goes to the custom box (so it's
        // never lost — e.g. a lone server whose pair isn't fully present).
        const claimed = new Set(serversForKeys(initSel));
        const initCustom = items.filter((x) => !claimed.has(x)).join("\n");
        load({ sel: initSel, custom: initCustom });
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const toggle = (key: string) =>
    setDraft((cur) => ({
      ...cur,
      sel: cur.sel.includes(key)
        ? cur.sel.filter((x) => x !== key)
        : [...cur.sel, key],
    }));

  const norm = (a: string[]) => [...a].sort().join("|");
  const dirty =
    norm(sel) !== norm(saved.sel) || custom.trim() !== saved.custom.trim();

  const save = () => {
    const customList = custom
      .split(/[\n,]/)
      .map((x) => x.trim())
      .filter(Boolean);
    const bad = customList.find((x) => !isValidDns(x));
    if (bad) {
      notifyError(`Неверный DNS-адрес: ${bad}`);
      return;
    }
    // Preset servers first (primary→secondary), then custom; dedupe across both.
    const all = [...serversForKeys(sel), ...customList];
    const deduped = [...new Set(all)];
    apply(async () => {
      await setXrayDNS(deduped.join("\n"));
      commit();
      notifySuccess("DNS сохранён");
    });
  };

  if (!loaded) return <CenterLoader />;

  return (
    <div className="pb-20">
      <Card className="p-4">
        <h3 className="mb-1 font-bold text-ink">DNS Серверы</h3>
        <p className="mb-3 text-sm text-ink-muted">
          Отметьте DNS-серверы, которые использует Xray. Один пункт добавляет
          основной и резервный адрес. Пусто — DNS по умолчанию.
        </p>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {POPULAR_DNS.map((d) => (
            <Checkbox
              key={d.key}
              checked={sel.includes(d.key)}
              onChange={() => toggle(d.key)}
              label={d.label}
            />
          ))}
        </div>
        <div className="mt-4">
          <p className="mb-1 text-sm font-medium text-ink">Свои серверы</p>
          <p className="mb-2 text-xs text-ink-muted">
            По одному в строке: IP, DoH-URL или localhost.
          </p>
          <textarea
            value={custom}
            onChange={(e) => setDraft((cur) => ({ ...cur, custom: e.currentTarget.value }))}
            rows={3}
            placeholder={"1.0.0.1\nhttps://dns.quad9.net/dns-query"}
            className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-sm outline-none focus:border-brand-500 focus:ring-2 focus:ring-brand-100"
          />
        </div>
      </Card>
      <SaveBar dirty={dirty} busy={applying} onSave={save} onCancel={reset} />
      <ApplyingModal open={applying} />
    </div>
  );
}
