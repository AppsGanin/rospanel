import { useEffect, useState } from "react";
import { getSettings, setXrayDNS } from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { notifyError, notifySuccess } from "./notify";
import { Card, CenterLoader, Checkbox, SaveBar } from "./ui";

// Popular Xray DNS resolvers offered as checkboxes.
const POPULAR_DNS: { value: string; label: string }[] = [
  { value: "1.1.1.1", label: "Cloudflare — 1.1.1.1" },
  { value: "https://cloudflare-dns.com/dns-query", label: "Cloudflare DoH" },
  { value: "8.8.8.8", label: "Google — 8.8.8.8" },
  { value: "https://dns.google/dns-query", label: "Google DoH" },
  { value: "9.9.9.9", label: "Quad9 — 9.9.9.9" },
  { value: "94.140.14.14", label: "AdGuard — 94.140.14.14" },
  { value: "77.88.8.8", label: "Яндекс — 77.88.8.8" },
];
const POPULAR_DNS_SET = new Set(POPULAR_DNS.map((d) => d.value));

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
  const [sel, setSel] = useState<string[]>([]);
  const [custom, setCustom] = useState("");
  const [saved, setSaved] = useState({ sel: [] as string[], custom: "" });

  useEffect(() => {
    getSettings()
      .then((s) => {
        const items = (s.xray_dns || "")
          .split(/[\n,]/)
          .map((x) => x.trim())
          .filter(Boolean);
        const initSel = items.filter((x) => POPULAR_DNS_SET.has(x));
        const initCustom = items
          .filter((x) => !POPULAR_DNS_SET.has(x))
          .join("\n");
        setSel(initSel);
        setCustom(initCustom);
        setSaved({ sel: initSel, custom: initCustom });
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  const toggle = (v: string) =>
    setSel((cur) =>
      cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v],
    );

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
    apply(async () => {
      await setXrayDNS([...sel, ...customList].join("\n"));
      setSaved({ sel, custom });
      notifySuccess("DNS сохранён");
    });
  };

  const cancel = () => {
    setSel(saved.sel);
    setCustom(saved.custom);
  };

  if (!loaded) return <CenterLoader />;

  return (
    <div className="pb-20">
      <Card className="p-4">
        <h3 className="mb-1 font-bold text-ink">DNS Серверы</h3>
        <p className="mb-3 text-sm text-ink-muted">
          Отметьте DNS-серверы, которые использует Xray. Пусто — DNS по
          умолчанию.
        </p>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {POPULAR_DNS.map((d) => (
            <Checkbox
              key={d.value}
              checked={sel.includes(d.value)}
              onChange={() => toggle(d.value)}
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
            onChange={(e) => setCustom(e.currentTarget.value)}
            rows={3}
            placeholder={"1.0.0.1\nhttps://dns.quad9.net/dns-query"}
            className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-sm outline-none focus:border-brand-500 focus:ring-2 focus:ring-brand-100"
          />
        </div>
      </Card>
      <SaveBar dirty={dirty} busy={applying} onSave={save} onCancel={cancel} />
      <ApplyingModal open={applying} />
    </div>
  );
}
