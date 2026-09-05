import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createInbound,
  deleteInbound,
  getInboundCatalog,
  listInbounds,
  regenInboundReality,
  updateInbound,
  type Inbound,
  type InboundCatalog,
  type InboundEnums,
  type InboundInput,
  type SockoptForm,
  type TLSExtraForm,
  type XHTTPExtraForm,
  type XmuxForm,
} from "./api";
import { ApplyingModal, useXrayApply } from "./apply";
import { useAction } from "./hooks";
import { NameVarsHint } from "./namevars";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  CenterLoader,
  IconChevron,
  Modal,
  Select,
  Switch,
  TagsInput,
  Textarea,
  TextInput,
} from "./ui";

// Labels for the wire values, so the UI never shows a bare protocol slug.
const PROTOCOL_LABELS: Record<string, string> = {
  vless: "VLESS",
  trojan: "Trojan",
  hysteria2: "Hysteria2",
  shadowsocks: "Shadowsocks",
};

const TRANSPORT_LABELS: Record<string, string> = {
  tcp: "TCP (raw)",
  ws: "WebSocket",
  xhttp: "XHTTP",
  grpc: "gRPC",
  httpupgrade: "HTTPUpgrade",
  hysteria: "QUIC / UDP",
};

const securityLabels = (): Record<string, string> => ({
  none: i18n.t("common.none"),
  tls: "TLS",
  reality: "REALITY",
});

const xhttpModeLabels = (): Record<string, string> => ({
  auto: i18n.t("inb.autoDefault"),
  "packet-up": "packet-up",
  "stream-up": "stream-up",
  "stream-one": "stream-one",
});

const hopIntervals = () =>
  ["5–10", "10–30", "30–60", "60–120"].map((range, i) => ({
    value: ["5-10", "10-30", "30-60", "60-120"][i],
    label: i18n.t("conn.sec", { range }),
  }));

// blank is a new inbound's starting point: the combination that works everywhere
// and needs the least explaining — VLESS over WebSocket behind our own certificate.
const blank = (): InboundInput => ({
  enabled: true,
  name: "",
  protocol: "vless",
  port: 0,
  transport: "ws",
  security: "tls",
  sni: "",
  fp: "firefox",
  path: "",
  host: "",
  mode: "",
  service_name: "",
  reality_dest: "",
  reality_anti_replay: false,
  hop_start: 0,
  hop_end: 0,
  hop_interval: "5-10",
  obfs: "",
  regen_obfs: false,
  header_type: "",
  header_hosts: [],
  header_paths: [],
  authority: "",
  multi_mode: false,
  method: "2022-blake3-aes-128-gcm",
  xhttp_extra: {},
  sockopt: {},
  tls_extra: {},
});

// toInput turns a stored inbound back into the editable shape.
function toInput(v: Inbound): InboundInput {
  const o = v.opts;
  return {
    enabled: v.enabled,
    name: v.name,
    protocol: v.protocol,
    port: v.port,
    transport: o.transport,
    security: o.security,
    sni: o.sni ?? "",
    fp: o.fp || "firefox",
    // The stored path carries its leading slash; the field is typed without one.
    path: (o.path ?? "").replace(/^\/+/, ""),
    host: o.host ?? "",
    mode: o.mode ?? "",
    service_name: o.service_name ?? "",
    reality_dest: o.reality_dest ?? "",
    reality_anti_replay: (o.reality_max_time_diff ?? 0) > 0,
    hop_start: o.hop_start ?? 0,
    hop_end: o.hop_end ?? 0,
    hop_interval: o.hop_interval || "5-10",
    obfs: o.obfs ?? "",
    // Never carried back from a stored inbound: regenerating is a fresh decision each
    // time the form is opened, not a sticky flag.
    regen_obfs: false,
    header_type: o.header_type ?? "",
    header_hosts: o.header_hosts ?? [],
    header_paths: (o.header_paths ?? []).map((p) => p.replace(/^\/+/, "")),
    authority: o.authority ?? "",
    multi_mode: o.multi_mode ?? false,
    method: o.method || "2022-blake3-aes-128-gcm",
    // The advanced sections come pre-disassembled from the server.
    xhttp_extra: v.xhttp_extra_form ?? {},
    sockopt: v.sockopt_form ?? {},
    tls_extra: v.tls_extra_form ?? {},
  };
}

function num(v: string): number {
  return Number(v.replace(/\D/g, "")) || 0;
}

// InboundsEditor is the per-server list of custom inbounds: the operator-defined
// listeners that sit beside the three built-in lanes. serverId is 0 for the master
// and the node id otherwise — an inbound belongs to exactly one machine.
//
// restartsPanel mirrors ConnectionsEditor: on the master a save reloads the panel's
// own Xray (so the "applying" modal waits for it to come back); for a node the panel
// only pushes the config and the node applies it itself.
export function InboundsEditor({
  serverId,
  restartsPanel,
}: {
  serverId: number;
  restartsPanel: boolean;
}) {
  const { t } = useTranslation();
  const [list, setList] = useState<Inbound[] | null>(null);
  const [catalog, setCatalog] = useState<InboundCatalog | null>(null);
  const [editing, setEditing] = useState<{ id: number; v: InboundInput } | null>(null);
  const [confirmDel, setConfirmDel] = useState<Inbound | null>(null);
  const { busy, run } = useAction();
  const { applying, apply: applyXray } = useXrayApply();

  const reload = () => listInbounds(serverId).then(setList);

  useEffect(() => {
    Promise.all([listInbounds(serverId), getInboundCatalog()])
      .then(([l, c]) => {
        setList(l);
        setCatalog(c);
      })
      .catch((e) => {
        notifyError(errMessage(e));
        setList([]);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverId]);

  // Every write here changes a listening socket, so it always goes through the
  // apply path on the master.
  const write = (fn: () => Promise<unknown>) => {
    const task = async () => {
      await fn();
      await reload();
      notifySuccess(t("common.saved"));
    };
    if (restartsPanel) applyXray(task);
    else run(task);
  };

  const save = () => {
    if (!editing) return;
    const v = editing.v;
    write(async () => {
      if (editing.id === 0) await createInbound(serverId, v);
      else await updateInbound(editing.id, v);
      setEditing(null);
    });
  };

  const toggle = (v: Inbound, enabled: boolean) =>
    write(() => updateInbound(v.id, { ...toInput(v), enabled }));

  const remove = (v: Inbound) =>
    write(async () => {
      await deleteInbound(v.id);
      setConfirmDel(null);
    });

  const regen = (v: Inbound) =>
    write(() => regenInboundReality(v.id));

  if (!list || !catalog) return <CenterLoader />;

  const full = list.length >= catalog.max;

  return (
    <div className="flex flex-col gap-3">
      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <h3 className="mb-1 font-bold text-ink">{t("inb.title")}</h3>
        <p className="text-sm text-ink-muted">
          {t("inb.description", { max: catalog.max })}
        </p>
      </div>

      {list.length === 0 && (
        <p className="px-1 text-sm text-ink-muted">
          {t("inb.empty")}
        </p>
      )}

      <div className="flex flex-col gap-2">
        {list.map((v) => (
          <InboundRow
            key={v.id}
            v={v}
            busy={busy || applying}
            onToggle={(en) => toggle(v, en)}
            onEdit={() => setEditing({ id: v.id, v: toInput(v) })}
            onDelete={() => setConfirmDel(v)}
            onRegen={() => regen(v)}
          />
        ))}
      </div>

      <div>
        <Button
          variant="light"
          onClick={() => setEditing({ id: 0, v: blank() })}
          disabled={busy || applying || full}
        >
          {t("inb.add")}
        </Button>
        {full && (
          <p className="mt-2 text-xs text-ink-muted">
            {t("inb.limitReached", { max: catalog.max })}
          </p>
        )}
      </div>

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={t(editing?.id ? "inb.connection" : "inb.newConnection")}
        size="lg"
      >
        {editing && (
          <InboundForm
            v={editing.v}
            catalog={catalog}
            onChange={(v) => setEditing({ ...editing, v })}
            onCancel={() => setEditing(null)}
            onSave={save}
            busy={busy || applying}
          />
        )}
      </Modal>

      <Modal
        open={!!confirmDel}
        onClose={() => setConfirmDel(null)}
        title={t("inb.deleteTitle")}
      >
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            {t("inb.deleteBody", { name: confirmDel?.name ?? "" })}
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setConfirmDel(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              color="red"
              loading={busy || applying}
              onClick={() => confirmDel && remove(confirmDel)}
            >
              {t("common.delete")}
            </Button>
          </div>
        </div>
      </Modal>
      <ApplyingModal open={applying} />
    </div>
  );
}

// InboundRow is one collapsed inbound: identity, where it listens, and the actions.
function InboundRow({
  v,
  busy,
  onToggle,
  onEdit,
  onDelete,
  onRegen,
}: {
  v: Inbound;
  busy: boolean;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
  onRegen: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const o = v.opts;
  const isSS = v.protocol === "shadowsocks";
  // Shadowsocks-2022 is encrypted by its own AEAD, so "no TLS" would misread as
  // insecure, and its transport slug just repeats the protocol. Show the method
  // instead — dropping the "2022-blake3-" prefix every method shares.
  const ssMethod = (o.method ?? "").replace("2022-blake3-", "");
  return (
    <div className="overflow-hidden rounded-xl border border-gray-200/80 bg-gray-50/60">
      <button
        type="button"
        onClick={() => setOpen((x) => !x)}
        className="flex w-full items-center justify-between gap-2 p-4 text-left"
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <IconChevron
            className={`shrink-0 text-gray-400 transition-transform ${open ? "rotate-180" : ""}`}
          />
          <span className="font-medium text-ink">{v.name}</span>
          <Badge color="gray">{PROTOCOL_LABELS[v.protocol] ?? v.protocol}</Badge>
          {!isSS && <Badge color="gray">{TRANSPORT_LABELS[o.transport] ?? o.transport}</Badge>}
          {isSS && ssMethod && <Badge color="green">{ssMethod}</Badge>}
          {o.security === "reality" && <Badge color="green">REALITY</Badge>}
          {o.security === "none" && !isSS && <Badge color="orange">{t("inb.noTls")}</Badge>}
          <Badge color="gray">{v.port}</Badge>
          {!v.enabled && <Badge color="gray">{t("conn.off")}</Badge>}
        </div>
        <span onClick={(e) => e.stopPropagation()} className="flex items-center">
          <Switch checked={v.enabled} onChange={onToggle} disabled={busy} />
        </span>
      </button>

      {open && (
        <div className="flex flex-col gap-3 border-t border-gray-100 px-4 pb-4 pt-3">
          {hasAdvanced(v) && (
            <p className="rounded-lg bg-gray-100 px-3 py-2 text-xs text-ink-muted">
              {t("inb.hasExtras")}
            </p>
          )}
          {v.unsupported && v.unsupported.length > 0 && (
            <p className="rounded-lg bg-orange-50 px-3 py-2 text-xs text-orange-800">
              {t("inb.unsupportedBy", { clients: v.unsupported.join(", ") })}
            </p>
          )}
          <div className="flex flex-col gap-1 text-sm">
            <Row label={t("conn.port")} value={String(v.port)} />
            {isSS ? (
              <Row label={t("inb.ssMethod")} value={o.method ?? ""} />
            ) : (
              <>
                <Row
                  label={t("conn.transport")}
                  value={TRANSPORT_LABELS[o.transport] ?? o.transport}
                />
                <Row
                  label={t("inb.security")}
                  value={securityLabels()[o.security] ?? o.security}
                />
              </>
            )}
            {o.path && <Row label={t("inb.path")} value={o.path} />}
            {o.service_name && <Row label={t("inb.grpcService")} value={o.service_name} />}
            {o.mode && <Row label={t("inb.xhttpMode")} value={o.mode} />}
            {o.header_type === "http" && (
              <Row label={t("inb.httpMasq")} value={(o.header_hosts ?? []).join(", ")} />
            )}
            {o.authority && <Row label="Authority" value={o.authority} />}
            {o.multi_mode && <Row label="gRPC" value="multi-mode" />}
            {o.host && <Row label="Host" value={o.host} />}
            {o.sni && <Row label="SNI" value={o.sni} />}
            {o.reality_dest && <Row label={t("inb.masquerade")} value={o.reality_dest} />}
            {(o.hop_end ?? 0) > v.port && (
              <Row label={t("inb.hop")} value={`${o.hop_start}–${o.hop_end}`} />
            )}
          </div>
          {o.security === "reality" && (
            <div className="flex flex-col gap-1 border-t border-gray-100 pt-3">
              <LongRow label="Public key" value={v.reality_public_key ?? ""} />
              <LongRow label="Short IDs" value={v.reality_short_id ?? ""} />
            </div>
          )}
          <div className="flex flex-wrap justify-end gap-2 border-t border-gray-100 pt-3">
            {o.security === "reality" && (
              <Button size="sm" variant="light" color="orange" onClick={onRegen} disabled={busy}>
                {t("conn.regenKeys")}
              </Button>
            )}
            <Button size="sm" variant="light" color="gray" onClick={onEdit} disabled={busy}>
              {t("common.edit")}
            </Button>
            <Button size="sm" variant="light" color="red" onClick={onDelete} disabled={busy}>
              {t("common.delete")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// hasAdvanced reports whether a stored inbound carries any advanced setting, for the
// collapsed-row hint. A form whose only content is an empty `raw` counts as none.
function hasAdvanced(v: Inbound): boolean {
  const nonEmpty = (o?: object) =>
    !!o &&
    Object.entries(o).some(([k, val]) => {
      if (k === "raw") return typeof val === "string" && val.trim() !== "";
      if (val === undefined || val === "" || val === false) return false;
      if (Array.isArray(val)) return val.length > 0;
      if (typeof val === "object") return Object.keys(val).length > 0;
      return true;
    });
  return (
    nonEmpty(v.xhttp_extra_form) ||
    nonEmpty(v.sockopt_form) ||
    nonEmpty(v.tls_extra_form) ||
    v.opts.header_type === "http" ||
    !!v.opts.authority ||
    !!v.opts.multi_mode
  );
}

// GroupLabel is a small heading inside the advanced section.
function GroupLabel({ children }: { children: React.ReactNode }) {
  return <p className="text-xs font-semibold uppercase tracking-wide text-ink-muted">{children}</p>;
}

// ANum is a number field bound to `number | undefined` — empty input means "unset"
// (omitted from the config), which is different from a real 0.
function ANum({ label, value, onChange, placeholder }: {
  label: string; value?: number; onChange: (v?: number) => void; placeholder?: string;
}) {
  return (
    <TextInput
      label={label}
      type="number"
      placeholder={placeholder}
      value={value === undefined ? "" : String(value)}
      onChange={(x) => {
        const t = x.trim();
        onChange(t === "" ? undefined : Number(t.replace(/[^\d-]/g, "")) || 0);
      }}
    />
  );
}

// ASel is a dropdown bound to `string | undefined`; the first "" option reads as
// the "default" option (Xray's own default).
function ASel({ label, value, onChange, options }: {
  label: string; value?: string; onChange: (v: string) => void; options: string[];
}) {
  const { t } = useTranslation();
  return (
    <Select
      label={label}
      value={value ?? ""}
      onChange={onChange}
      data={options.map((o) => ({ value: o, label: o === "" ? t("inb.byDefault") : o }))}
    />
  );
}

// ASw is a switch. onChange gets a plain boolean; the caller decides whether "off"
// means false or "unset" (undefined) depending on the field's Go type.
function ASw({ label, hint, value, onChange }: {
  label: string; hint?: string; value?: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-center justify-between gap-3">
      <span className="text-sm">
        {label}
        {hint && <span className="block text-xs text-ink-muted">{hint}</span>}
      </span>
      <Switch checked={!!value} onChange={onChange} />
    </label>
  );
}

// HeadersEditor edits an arbitrary key/value map (XHTTP request headers). It keeps its
// own list state so renaming a key mid-type doesn't reorder or lose focus; the
// assembled object is pushed up on every change (blank keys dropped).
function HeadersEditor({ value, onChange }: {
  value?: Record<string, string>;
  onChange: (v?: Record<string, string>) => void;
}) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<[string, string][]>(() => Object.entries(value ?? {}));
  const push = (next: [string, string][]) => {
    setRows(next);
    const obj: Record<string, string> = {};
    for (const [k, val] of next) if (k.trim()) obj[k.trim()] = val;
    onChange(Object.keys(obj).length ? obj : undefined);
  };
  return (
    <div className="flex flex-col gap-2">
      <span className="text-sm text-ink-muted">{t("inb.requestHeaders")}</span>
      {rows.map(([k, val], i) => (
        <div key={i} className="flex items-center gap-2">
          <TextInput
            value={k}
            placeholder={t("inb.headerName")}
            onChange={(x) => push(rows.map((r, j) => (j === i ? [x, r[1]] : r)))}
          />
          <TextInput
            value={val}
            placeholder={t("inb.headerValue")}
            onChange={(x) => push(rows.map((r, j) => (j === i ? [r[0], x] : r)))}
          />
          <Button size="sm" variant="light" color="red" onClick={() => push(rows.filter((_, j) => j !== i))}>
            ×
          </Button>
        </div>
      ))}
      <div>
        <Button size="sm" variant="light" onClick={() => push([...rows, ["", ""]])}>
          {t("inb.addHeader")}
        </Button>
      </div>
    </div>
  );
}

// RawFallback is the per-section escape hatch: a JSON object of any key the panel
// doesn't surface as a field (the exotic structured ones + anything a future Xray
// adds). Collapsed; surfaced fields win over it on save.
function RawFallback({ value, onChange, example }: {
  value?: string;
  onChange: (v: string) => void;
  example?: string;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(!!value);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((x) => !x)}
        className="flex items-center gap-1 text-xs text-ink-muted"
      >
        <IconChevron className={`shrink-0 transition-transform ${open ? "rotate-180" : ""}`} />
        {t("inb.rawJson")}
      </button>
      {open && (
        <Textarea
          rows={3}
          value={value ?? ""}
          onChange={onChange}
          placeholder={example ?? "{ }"}
        />
      )}
    </div>
  );
}

// AdvancedSection exposes every transport knob from Xray's inbound reference as a
// typed control. Collapsed by default: none of it is needed for a working lane, and
// all of it can break one, so the panel still runs the whole config through
// `xray run -test` before it saves.
//
// Grouped by whether the CLIENT must know the value: the XHTTP extra / masquerade /
// gRPC knobs are mirrored into the generated links, sockopt and extra-TLS are
// server-only. Each JSON section keeps a "raw" escape hatch for anything not surfaced.
function AdvancedSection({
  v,
  set,
  enums,
}: {
  v: InboundInput;
  set: <K extends keyof InboundInput>(k: K, val: InboundInput[K]) => void;
  enums: InboundEnums;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  if (v.protocol === "hysteria2") return null; // Hysteria2 has no transport to tune

  const x = v.xhttp_extra;
  const setX = <K extends keyof XHTTPExtraForm>(k: K, val: XHTTPExtraForm[K]) =>
    set("xhttp_extra", { ...x, [k]: val });
  const xmux = x.xmux ?? {};
  const setXmux = <K extends keyof XmuxForm>(k: K, val: XmuxForm[K]) =>
    setX("xmux", { ...xmux, [k]: val });
  const s = v.sockopt;
  const setS = <K extends keyof SockoptForm>(k: K, val: SockoptForm[K]) =>
    set("sockopt", { ...s, [k]: val });
  const tlsx = v.tls_extra;
  const setT = <K extends keyof TLSExtraForm>(k: K, val: TLSExtraForm[K]) =>
    set("tls_extra", { ...tlsx, [k]: val });
  // off → undefined for the *bool fields (sockopt / TLS), so the config carries no
  // explicit `false`; XHTTP bools are plain bools and drop false on their own.
  const off = (b: boolean) => (b ? true : undefined);

  return (
    <div className="border-t border-gray-100 pt-3">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 text-left text-sm font-medium text-ink"
      >
        <IconChevron className={`shrink-0 text-gray-400 transition-transform ${open ? "rotate-180" : ""}`} />
        {t("inb.extraParams")}
      </button>

      {open && (
        <div className="mt-3 flex flex-col gap-5">
          {v.transport === "tcp" && (
            <div className="flex flex-col gap-3">
              <GroupLabel>{t("inb.httpMasq")}</GroupLabel>
              <Select
                label={t("inb.type")}
                value={v.header_type || "none"}
                onChange={(hx) => set("header_type", hx === "none" ? "" : hx)}
                data={[
                  { value: "none", label: t("inb.masqOff") },
                  { value: "http", label: t("inb.masqHttp") },
                ]}
              />
              {v.header_type === "http" && (
                <>
                  <TagsInput
                    label={t("inb.masqHosts")}
                    value={v.header_hosts}
                    onChange={(hx) => set("header_hosts", hx)}
                    placeholder={t("inb.hostPlaceholder")}
                  />
                  <TagsInput
                    label={t("inb.masqPaths")}
                    value={v.header_paths}
                    onChange={(hx) => set("header_paths", hx)}
                    placeholder={t("inb.pathPlaceholder")}
                  />
                </>
              )}
            </div>
          )}

          {v.transport === "grpc" && (
            <div className="flex flex-col gap-3">
              <GroupLabel>gRPC</GroupLabel>
              <TextInput
                label={t("inb.authority")}
                value={v.authority}
                onChange={(gx) => set("authority", gx)}
                placeholder="grpc.example.com"
              />
              <ASw label="Multi-mode" value={v.multi_mode} onChange={(b) => set("multi_mode", b)} />
            </div>
          )}

          {v.transport === "xhttp" && (
            <div className="flex flex-col gap-4">
              <GroupLabel>XHTTP</GroupLabel>
              <p className="text-xs font-medium text-ink-muted">{t("inb.xmux")}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <TextInput label="maxConcurrency" value={xmux.maxConcurrency ?? ""} onChange={(z) => setXmux("maxConcurrency", z)} placeholder="8-32" />
                <TextInput label="maxConnections" value={xmux.maxConnections ?? ""} onChange={(z) => setXmux("maxConnections", z)} />
                <TextInput label="cMaxReuseTimes" value={xmux.cMaxReuseTimes ?? ""} onChange={(z) => setXmux("cMaxReuseTimes", z)} placeholder="36-96" />
                <TextInput label="hMaxRequestTimes" value={xmux.hMaxRequestTimes ?? ""} onChange={(z) => setXmux("hMaxRequestTimes", z)} />
                <TextInput label="hMaxReusableSecs" value={xmux.hMaxReusableSecs ?? ""} onChange={(z) => setXmux("hMaxReusableSecs", z)} />
                <ANum label="hKeepAlivePeriod" value={xmux.hKeepAlivePeriod} onChange={(z) => setXmux("hKeepAlivePeriod", z)} />
              </div>

              <p className="text-xs font-medium text-ink-muted">{t("inb.padding")}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <TextInput label="xPaddingBytes" value={x.xPaddingBytes ?? ""} onChange={(z) => setX("xPaddingBytes", z)} placeholder="100-1000" />
                <ASel label="xPaddingPlacement" value={x.xPaddingPlacement} onChange={(z) => setX("xPaddingPlacement", z)} options={enums.placements} />
                <TextInput label="xPaddingMethod" value={x.xPaddingMethod ?? ""} onChange={(z) => setX("xPaddingMethod", z)} placeholder="tokenish" />
                <TextInput label="xPaddingKey" value={x.xPaddingKey ?? ""} onChange={(z) => setX("xPaddingKey", z)} />
                <TextInput label="xPaddingHeader" value={x.xPaddingHeader ?? ""} onChange={(z) => setX("xPaddingHeader", z)} />
              </div>
              <ASw label="xPaddingObfsMode" value={x.xPaddingObfsMode} onChange={(b) => setX("xPaddingObfsMode", b)} />

              <p className="text-xs font-medium text-ink-muted">{t("inb.sessionIds")}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <ASel label="sessionIDPlacement" value={x.sessionIDPlacement} onChange={(z) => setX("sessionIDPlacement", z)} options={enums.placements} />
                <TextInput label="sessionIDKey" value={x.sessionIDKey ?? ""} onChange={(z) => setX("sessionIDKey", z)} placeholder="auth" />
                <TextInput label="sessionIDTable" value={x.sessionIDTable ?? ""} onChange={(z) => setX("sessionIDTable", z)} placeholder="alphabet" />
                <TextInput label="sessionIDLength" value={x.sessionIDLength ?? ""} onChange={(z) => setX("sessionIDLength", z)} placeholder="20" />
                <ASel label="seqPlacement" value={x.seqPlacement} onChange={(z) => setX("seqPlacement", z)} options={enums.placements} />
                <TextInput label="seqKey" value={x.seqKey ?? ""} onChange={(z) => setX("seqKey", z)} placeholder="id" />
              </div>

              <p className="text-xs font-medium text-ink-muted">{t("inb.uplink")}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <ASel label="uplinkHTTPMethod" value={x.uplinkHTTPMethod} onChange={(z) => setX("uplinkHTTPMethod", z)} options={enums.uplink_methods} />
                <ASel label="uplinkDataPlacement" value={x.uplinkDataPlacement} onChange={(z) => setX("uplinkDataPlacement", z)} options={enums.placements} />
                <TextInput label="uplinkDataKey" value={x.uplinkDataKey ?? ""} onChange={(z) => setX("uplinkDataKey", z)} />
                <TextInput label="uplinkChunkSize" value={x.uplinkChunkSize ?? ""} onChange={(z) => setX("uplinkChunkSize", z)} />
              </div>

              <p className="text-xs font-medium text-ink-muted">{t("inb.flowControl")}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <TextInput label="scMaxEachPostBytes" value={x.scMaxEachPostBytes ?? ""} onChange={(z) => setX("scMaxEachPostBytes", z)} />
                <TextInput label="scMinPostsIntervalMs" value={x.scMinPostsIntervalMs ?? ""} onChange={(z) => setX("scMinPostsIntervalMs", z)} />
                <ANum label="scMaxBufferedPosts" value={x.scMaxBufferedPosts} onChange={(z) => setX("scMaxBufferedPosts", z)} />
                <TextInput label="scStreamUpServerSecs" value={x.scStreamUpServerSecs ?? ""} onChange={(z) => setX("scStreamUpServerSecs", z)} placeholder="20-80" />
                <ANum label="serverMaxHeaderBytes" value={x.serverMaxHeaderBytes} onChange={(z) => setX("serverMaxHeaderBytes", z)} />
              </div>

              <ASw label="noGRPCHeader" value={x.noGRPCHeader} onChange={(b) => setX("noGRPCHeader", b)} />
              <ASw label="noSSEHeader" value={x.noSSEHeader} onChange={(b) => setX("noSSEHeader", b)} />

              <HeadersEditor value={x.headers} onChange={(h) => setX("headers", h)} />
              <RawFallback value={x.raw} onChange={(z) => setX("raw", z)} />
            </div>
          )}

          {v.security === "tls" && (
            <div className="flex flex-col gap-3">
              <GroupLabel>{t("inb.extraTls")}</GroupLabel>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <ASel label="minVersion" value={tlsx.minVersion} onChange={(z) => setT("minVersion", z)} options={enums.tls_versions} />
                <ASel label="maxVersion" value={tlsx.maxVersion} onChange={(z) => setT("maxVersion", z)} options={enums.tls_versions} />
              </div>
              <TextInput label="cipherSuites" value={tlsx.cipherSuites ?? ""} onChange={(z) => setT("cipherSuites", z)} />
              <TagsInput label="curvePreferences" value={tlsx.curvePreferences ?? []} onChange={(z) => setT("curvePreferences", z.length ? z : undefined)} placeholder={t("inb.curvePlaceholder")} />
              <TagsInput label="verifyPeerCertByName" value={tlsx.verifyPeerCertByName ?? []} onChange={(z) => setT("verifyPeerCertByName", z.length ? z : undefined)} placeholder={t("inb.verifyPlaceholder")} />
              <ASw label="rejectUnknownSni" value={tlsx.rejectUnknownSni} onChange={(b) => setT("rejectUnknownSni", off(b))} />
              <ASw label="enableSessionResumption" value={tlsx.enableSessionResumption} onChange={(b) => setT("enableSessionResumption", off(b))} />
              <ASw label="disableSystemRoot" value={tlsx.disableSystemRoot} onChange={(b) => setT("disableSystemRoot", off(b))} />
              <RawFallback value={tlsx.raw} onChange={(z) => setT("raw", z)} />
            </div>
          )}

          <div className="flex flex-col gap-3">
            <GroupLabel>sockopt</GroupLabel>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <TextInput label="tcpCongestion" value={s.tcpCongestion ?? ""} onChange={(z) => setS("tcpCongestion", z)} placeholder="bbr" />
              <ASel label="domainStrategy" value={s.domainStrategy} onChange={(z) => setS("domainStrategy", z)} options={enums.domain_strategy} />
              <ASel label="tproxy" value={s.tproxy} onChange={(z) => setS("tproxy", z)} options={enums.tproxy} />
              <ASel label="addressPortStrategy" value={s.addressPortStrategy} onChange={(z) => setS("addressPortStrategy", z)} options={enums.address_port_strategy} />
              <TextInput label="interface" value={s.interface ?? ""} onChange={(z) => setS("interface", z)} />
              <TextInput label="dialerProxy" value={s.dialerProxy ?? ""} onChange={(z) => setS("dialerProxy", z)} />
              <ANum label="mark" value={s.mark} onChange={(z) => setS("mark", z)} />
              <ANum label="tcpKeepAliveIdle" value={s.tcpKeepAliveIdle} onChange={(z) => setS("tcpKeepAliveIdle", z)} />
              <ANum label="tcpKeepAliveInterval" value={s.tcpKeepAliveInterval} onChange={(z) => setS("tcpKeepAliveInterval", z)} />
              <ANum label="tcpUserTimeout" value={s.tcpUserTimeout} onChange={(z) => setS("tcpUserTimeout", z)} />
              <ANum label="tcpMaxSeg" value={s.tcpMaxSeg} onChange={(z) => setS("tcpMaxSeg", z)} />
              <ANum label="tcpWindowClamp" value={s.tcpWindowClamp} onChange={(z) => setS("tcpWindowClamp", z)} />
            </div>
            <ASw label="tcpMptcp" value={s.tcpMptcp} onChange={(b) => setS("tcpMptcp", off(b))} />
            <ASw label="tcpFastOpen" value={s.tcpFastOpen} onChange={(b) => setS("tcpFastOpen", off(b))} />
            <ASw label="v6only" value={s.v6only} onChange={(b) => setS("v6only", off(b))} />
            <ASw label="penetrate" value={s.penetrate} onChange={(b) => setS("penetrate", off(b))} />
            <RawFallback value={s.raw} onChange={(z) => setS("raw", z)} example={'{\n  "customSockopt": []\n}'} />
          </div>
        </div>
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-ink-muted">{label}</span>
      <span className="text-right font-medium">{value}</span>
    </div>
  );
}

function LongRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-sm text-ink-muted">{label}</span>
      <code className="block break-all rounded border border-gray-200 bg-white/60 px-2 py-1 font-mono text-xs text-ink">
        {value}
      </code>
    </div>
  );
}

// InboundForm renders exactly the fields the chosen protocol × transport × security
// actually uses. The options come from the server's catalog, so the form can never
// offer a combination the validator would reject.
function InboundForm({
  v,
  catalog,
  onChange,
  onCancel,
  onSave,
  busy,
}: {
  v: InboundInput;
  catalog: InboundCatalog;
  onChange: (v: InboundInput) => void;
  onCancel: () => void;
  onSave: () => void;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const set = <K extends keyof InboundInput>(k: K, val: InboundInput[K]) =>
    onChange({ ...v, [k]: val });

  const transports = useMemo(
    () => catalog.combos.filter((c) => c.protocol === v.protocol),
    [catalog, v.protocol],
  );
  const combo = transports.find((c) => c.transport === v.transport);
  const securities = combo?.securities ?? [];

  // Switching protocol or transport can land on a combination that no longer
  // accepts the current transport/security; snap to the first valid one so the form
  // never shows a selection the server would reject.
  const pickProtocol = (p: string) => {
    const first = catalog.combos.find((c) => c.protocol === p);
    onChange({
      ...v,
      protocol: p,
      transport: first?.transport ?? "",
      security: first?.securities[0] ?? "tls",
    });
  };
  const pickTransport = (t: string) => {
    const c = transports.find((x) => x.transport === t);
    onChange({
      ...v,
      transport: t,
      security: c?.securities.includes(v.security) ? v.security : (c?.securities[0] ?? "tls"),
    });
  };

  const isHysteria = v.protocol === "hysteria2";
  const isShadowsocks = v.protocol === "shadowsocks";
  // Both protocols own their transport and security, so the editor shows neither
  // control for them — the difference from every other protocol is just this flag.
  const fixedTransport = isHysteria || isShadowsocks;
  const usesPath = ["ws", "xhttp", "httpupgrade"].includes(v.transport);
  const dests = v.reality_dest
    ? v.reality_dest.split(",").map((d) => d.trim()).filter(Boolean)
    : [];

  return (
    <div className="flex flex-col gap-3">
      <TextInput
        label={t("groups.name")}
        value={v.name}
        onChange={(x) => set("name", x)}
        placeholder={t("inb.namePlaceholder")}
      />
      <p className="-mt-1 text-xs text-ink-muted">
        {t("inb.nameHint")}
      </p>
      <NameVarsHint onInsert={(x) => set("name", (v.name + " " + x).trim())} />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Select
          label={t("inb.protocol")}
          value={v.protocol}
          onChange={pickProtocol}
          data={catalog.protocols.map((p) => ({
            value: p,
            label: PROTOCOL_LABELS[p] ?? p,
          }))}
        />
        <TextInput
          label={t("conn.port")}
          type="number"
          value={String(v.port || "")}
          onChange={(x) => set("port", num(x))}
        />
      </div>

      {!fixedTransport && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Select
            label={t("conn.transport")}
            value={v.transport}
            onChange={pickTransport}
            data={transports.map((c) => ({
              value: c.transport,
              label: TRANSPORT_LABELS[c.transport] ?? c.transport,
            }))}
          />
          <Select
            label={t("inb.security")}
            value={v.security}
            onChange={(x) => set("security", x)}
            data={securities.map((s) => ({
              value: s,
              label: securityLabels()[s] ?? s,
            }))}
          />
        </div>
      )}

      {isShadowsocks && (
        <div className="flex flex-col gap-2">
          <Select
            label={t("inb.ssMethod")}
            value={v.method}
            onChange={(x) => set("method", x)}
            data={(catalog.enums.ss_methods ?? []).map((m) => ({ value: m, label: m }))}
          />
          <p className="text-xs text-ink-muted">{t("inb.ssHint")}</p>
        </div>
      )}

      {combo?.unsupported && combo.unsupported.length > 0 && (
        <p className="rounded-lg bg-orange-50 px-3 py-2 text-xs text-orange-800">
          {t("inb.comboUnsupported", { clients: combo.unsupported.join(", ") })}
        </p>
      )}

      {usesPath && (
        <div className="flex flex-col gap-2">
          <TextInput
            label={t("inb.path")}
            value={v.path}
            onChange={(x) => set("path", x.replace(/^\/+/, ""))}
            placeholder="secret"
          />
          <p className="text-xs text-ink-muted">
            {t("inb.pathHint")}
          </p>
        </div>
      )}

      {v.transport === "grpc" && (
        <TextInput
          label={t("inb.grpcServiceName")}
          value={v.service_name}
          onChange={(x) => set("service_name", x)}
          placeholder="secretsvc"
        />
      )}

      {v.transport === "xhttp" && (
        <Select
          label={t("inb.xhttpMode")}
          value={v.mode || "auto"}
          onChange={(x) => set("mode", x === "auto" ? "" : x)}
          data={catalog.xhttp_modes.map((m) => ({
            value: m,
            label: xhttpModeLabels()[m] ?? m,
          }))}
        />
      )}

      {!fixedTransport && v.security !== "reality" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <TextInput
            label={t("inb.sni")}
            value={v.sni}
            onChange={(x) => set("sni", x)}
            placeholder="example.com"
          />
          {usesPath && (
            <TextInput
              label={t("inb.host")}
              value={v.host}
              onChange={(x) => set("host", x)}
              placeholder="example.com"
            />
          )}
        </div>
      )}

      {!fixedTransport && (
        <Select
          label="Fingerprint (uTLS)"
          value={v.fp || "firefox"}
          onChange={(x) => set("fp", x)}
          data={catalog.fingerprints.map((f) => ({
            value: f,
            label: f.charAt(0).toUpperCase() + f.slice(1),
          }))}
        />
      )}

      {v.security === "reality" && (
        <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
          <TagsInput
            label={t("inb.realityDest")}
            value={dests}
            onChange={(x) => set("reality_dest", x.join(","))}
            placeholder={t("inb.realityPlaceholder")}
          />
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm">
              {t("conn.antiReplay")}
              <span className="block text-xs text-ink-muted">
                {t("conn.antiReplayHint")}
              </span>
            </span>
            <Switch
              checked={v.reality_anti_replay}
              onChange={(x) => set("reality_anti_replay", x)}
            />
          </label>
          <p className="text-xs text-ink-muted">
            {t("inb.realityHint")}
          </p>
        </div>
      )}

      {isHysteria && (
        <div className="flex flex-col gap-3 border-t border-gray-100 pt-3">
          <div className="grid grid-cols-2 gap-3">
            <TextInput
              label={t("conn.hopFrom")}
              type="number"
              value={String(v.hop_start || "")}
              onChange={(x) => set("hop_start", num(x))}
            />
            <TextInput
              label={t("conn.hopTo")}
              type="number"
              value={String(v.hop_end || "")}
              onChange={(x) => set("hop_end", num(x))}
            />
          </div>
          <Select
            label={t("conn.hopInterval")}
            value={v.hop_interval || "5-10"}
            onChange={(x) => set("hop_interval", x)}
            data={hopIntervals()}
          />
          <p className="text-xs text-ink-muted">
            {t("inb.hopHint")}
          </p>
          {/* Salamander: shared with the client, so it is shown, not masked — but read
              only. The key is minted by the server on save (regen_obfs), never typed,
              the same way this inbound's REALITY material is. */}
          <div className="flex flex-col gap-1">
            <span className="text-sm text-ink-muted">{t("conn.obfs")}</span>
            <code className="block break-all rounded border border-gray-200 bg-white/60 px-2 py-1 font-mono text-xs text-ink">
              {v.regen_obfs ? t("conn.obfsWillRegen") : v.obfs || t("conn.obfsOff")}
            </code>
          </div>
          <div className="flex items-center gap-2">
            <p className="flex-1 text-xs text-ink-muted">{t("conn.obfsHint")}</p>
            <Button
              variant="subtle"
              size="xs"
              color={v.regen_obfs ? "orange" : "gray"}
              onClick={() => set("regen_obfs", !v.regen_obfs)}
            >
              {t("conn.obfsGenerate")}
            </Button>
            {(v.obfs !== "" || v.regen_obfs) && (
              <Button
                variant="subtle"
                size="xs"
                onClick={() => {
                  set("obfs", "");
                  set("regen_obfs", false);
                }}
              >
                {t("conn.obfsDisable")}
              </Button>
            )}
          </div>
        </div>
      )}

      {/* Shadowsocks has no streamSettings, so none of the transport/TLS/masquerade
          knobs in here apply to it. */}
      {!isShadowsocks && <AdvancedSection v={v} set={set} enums={catalog.enums} />}

      <label className="flex items-center justify-between gap-3 border-t border-gray-100 pt-3">
        <span className="text-sm">{t("common.enabled")}</span>
        <Switch checked={v.enabled} onChange={(x) => set("enabled", x)} />
      </label>

      <div className="flex justify-end gap-2 border-t border-gray-100 pt-3">
        <Button variant="light" color="gray" onClick={onCancel} disabled={busy}>
          {t("common.cancel")}
        </Button>
        <Button onClick={onSave} loading={busy}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}
