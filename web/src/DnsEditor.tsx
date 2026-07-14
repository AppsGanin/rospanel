import { useEffect, useState } from "react";
import { Checkbox } from "./ui";

// DnsPreset contributes its servers IN ORDER — primary first, then the secondary —
// so ticking one preset gives redundancy in a single click. Xray queries them in the
// listed order.
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

// splitItems parses a DNS string (newline/comma separated) into trimmed entries.
const splitItems = (s: string) =>
  (s || "")
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);

// parseDns derives the ticked presets + leftover custom lines from a stored DNS
// string. A preset is selected when ALL of its servers are present; anything not
// claimed by a selected preset falls to the custom box (so nothing is ever lost).
function parseDns(value: string): { sel: string[]; custom: string } {
  const items = splitItems(value);
  const itemSet = new Set(items);
  const sel = POPULAR_DNS.filter((p) =>
    p.servers.every((srv) => itemSet.has(srv)),
  ).map((p) => p.key);
  const claimed = new Set(serversForKeys(sel));
  const custom = items.filter((x) => !claimed.has(x)).join("\n");
  return { sel, custom };
}

// combineDns rebuilds the stored DNS string: preset servers first (primary→secondary),
// then custom lines, deduped across both.
function combineDns(sel: string[], custom: string): string {
  const all = [...serversForKeys(sel), ...splitItems(custom)];
  return [...new Set(all)].join("\n");
}

// canonicalDns normalizes a stored DNS string to exactly what this editor emits
// (deduped, newline-joined, presets first). Callers seed their dirty-tracking baseline
// through it so a non-canonical stored value (e.g. comma-separated) doesn't read as
// "changed" the moment the editor round-trips it.
export const canonicalDns = (value: string): string => {
  const { sel, custom } = parseDns(value);
  return combineDns(sel, custom);
};

// DnsEditor is a controlled DNS picker: preset checkboxes + a free-form custom box.
// It holds the derived sel/custom locally (seeded once from `value`) and emits the
// recombined DNS string on every change — so the container just stores a string and
// saves it. Empty ⇒ Xray's default resolver.
export function DnsEditor({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const [st, setSt] = useState(() => parseDns(value));

  // Re-derive from `value` when the container changes it out from under us (e.g. the
  // "Отменить" reset restores the last-saved DNS) — but not for our own edits, whose
  // recombined string already equals `value`, so the parse is skipped.
  useEffect(() => {
    if (value !== combineDns(st.sel, st.custom)) setSt(parseDns(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const emit = (sel: string[], custom: string) => {
    setSt({ sel, custom });
    onChange(combineDns(sel, custom));
  };
  const toggle = (key: string) =>
    emit(
      st.sel.includes(key)
        ? st.sel.filter((k) => k !== key)
        : [...st.sel, key],
      st.custom,
    );

  return (
    <div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {POPULAR_DNS.map((d) => (
          <Checkbox
            key={d.key}
            checked={st.sel.includes(d.key)}
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
          value={st.custom}
          onChange={(e) => emit(st.sel, e.currentTarget.value)}
          rows={3}
          placeholder={"1.0.0.1\nhttps://dns.quad9.net/dns-query"}
          className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-sm outline-none focus:border-brand-500 focus:ring-2 focus:ring-brand-100"
        />
      </div>
    </div>
  );
}
