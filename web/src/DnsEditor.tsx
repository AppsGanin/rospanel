import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn, MICRO, Mono, SettingRow, Textarea, useReadOnly } from "./ui";

// A provider offers one or more variants — plain addresses, DoH, a regional DoH — and
// each variant is a preset that contributes its servers IN ORDER (primary first, then
// the secondary), so ticking one gives redundancy in a single click. Xray queries them
// in the listed order.
type DnsPreset = { key: string; label: string; servers: string[] };
type DnsGroup = "general" | "adblock" | "unblock";
type DnsProvider = { name: string; group: DnsGroup; variants: DnsPreset[] };

const ip = (key: string, ...servers: string[]): DnsPreset => ({ key, label: "IP", servers });
const doh = (key: string, url: string, label = "DoH"): DnsPreset => ({ key, label, servers: [url] });

const PROVIDERS: DnsProvider[] = [
  { name: "Cloudflare", group: "general", variants: [ip("cloudflare", "1.1.1.1", "1.0.0.1"), doh("cloudflare-doh", "https://cloudflare-dns.com/dns-query")] },
  { name: "Google", group: "general", variants: [ip("google", "8.8.8.8", "8.8.4.4"), doh("google-doh", "https://dns.google/dns-query")] },
  { name: "Quad9", group: "general", variants: [ip("quad9", "9.9.9.9", "149.112.112.112"), doh("quad9-doh", "https://dns.quad9.net/dns-query")] },
  { name: "AdGuard Unfiltered", group: "general", variants: [ip("adguard-unfiltered", "94.140.14.140", "94.140.14.141"), doh("adguard-unfiltered-doh", "https://unfiltered.adguard-dns.com/dns-query")] },
  { name: "Yandex", group: "general", variants: [ip("yandex", "77.88.8.8", "77.88.8.1"), doh("yandex-doh", "https://common.dot.dns.yandex.net/dns-query")] },
  { name: "AdGuard", group: "adblock", variants: [ip("adguard", "94.140.14.14", "94.140.15.15"), doh("adguard-doh", "https://dns.adguard-dns.com/dns-query")] },
  {
    name: "GeoHide",
    group: "unblock",
    variants: [
      ip("geohide", "159.194.200.33", "37.230.192.51"),
      doh("geohide-doh", "https://geohide.ru/dns-query"),
      doh("geohide-eu-doh", "https://eu.geohide.ru/dns-query", "DoH EU"),
      doh("geohide-us-doh", "https://us.geohide.ru/dns-query", "DoH US"),
    ],
  },
  { name: "Comss", group: "unblock", variants: [ip("comss", "83.220.169.155", "212.109.195.93"), doh("comss-doh", "https://dns.comss.one/dns-query")] },
  { name: "Malw", group: "unblock", variants: [ip("malw", "179.254.166.171", "164.5.224.81"), doh("malw-doh", "https://dns.malw.link/dns-query")] },
];

const GROUPS: DnsGroup[] = ["general", "adblock", "unblock"];

const POPULAR_DNS: DnsPreset[] = PROVIDERS.flatMap((p) => p.variants);

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
  const { t } = useTranslation();
  const [st, setSt] = useState(() => parseDns(value));

  // Re-derive from `value` when the container changes it out from under us (e.g. the
  // the Cancel reset restores the last-saved DNS) — but not for our own edits, whose
  // recombined string already equals `value`, so the parse is skipped.
  // biome-ignore lint/correctness/useExhaustiveDependencies: watches the container's value only — st is what this compares it against, and listing it would re-parse our own edits back over the draft
  useEffect(() => {
    if (value !== combineDns(st.sel, st.custom)) setSt(parseDns(value));
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

  const groupTitle: Record<DnsGroup, string> = {
    general: t("dns.groupGeneral"),
    adblock: t("dns.groupAdblock"),
    unblock: t("dns.groupUnblock"),
  };

  return (
    <>
      <SettingRow>
        <div className="flex flex-col gap-3">
          {GROUPS.map((g) => (
            <div key={g}>
              <p className={cn(MICRO, "mb-1.5")}>{groupTitle[g]}</p>
              <div className="divide-y divide-gray-100 rounded-xl border border-gray-200">
                {PROVIDERS.filter((p) => p.group === g).map((p) => {
                  const on = p.variants.some((v) => st.sel.includes(v.key));
                  const addrs = p.variants.find((v) => v.label === "IP")?.servers;
                  return (
                    <div key={p.name} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2">
                      {/* basis-44: room for the addresses, or the variants wrap below them
                          rather than pressing them under the chips. */}
                      <div className="min-w-0 flex-1 basis-44">
                        <p className={cn("text-sm", on ? "font-semibold text-ink" : "text-ink")}>{p.name}</p>
                        {addrs && <Mono className="block text-[11px] text-ink-muted">{addrs.join(" · ")}</Mono>}
                      </div>
                      <div className="flex flex-wrap gap-1.5">
                        {p.variants.map((v) => (
                          <VariantChip
                            key={v.key}
                            label={v.label}
                            title={v.servers.join("\n")}
                            checked={st.sel.includes(v.key)}
                            onToggle={() => toggle(v.key)}
                          />
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </SettingRow>
      <SettingRow label={t("dns.customServers")} hint={t("dns.customHint")}>
        <Textarea
          value={st.custom}
          onChange={(v) => emit(st.sel, v)}
          rows={3}
          mono
          placeholder={"1.0.0.1\nhttps://dns.quad9.net/dns-query"}
        />
      </SettingRow>
    </>
  );
}

// VariantChip switches one variant of a provider on or off.
function VariantChip({
  label,
  title,
  checked,
  onToggle,
}: {
  label: string;
  title: string;
  checked: boolean;
  onToggle: () => void;
}) {
  const ro = useReadOnly();
  return (
    <button
      type="button"
      aria-pressed={checked}
      title={title}
      disabled={ro}
      onClick={onToggle}
      className={cn(
        "inline-flex h-7 items-center rounded-full border bg-white px-2.5 text-xs font-medium transition disabled:opacity-60",
        checked
          ? "border-accent text-accent"
          : "border-gray-200 text-ink-muted hover:border-gray-300 hover:text-ink",
      )}
    >
      {label}
    </button>
  );
}
