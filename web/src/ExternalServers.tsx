import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createExternal,
  deleteExternal,
  EMPTY_EXT_IDENTITY,
  type ExtIdentity,
  type ExtServer,
  type ExtSubscription,
  getExternal,
  listNodes,
  type NodeView,
  setExternalEnabled,
  setExternalRelay,
  setExternalServerEnabled,
  setExternalServersEnabled,
  syncExternal,
  updateExternalSource,
} from "./api";
import { useAction, useShowMore } from "./hooks";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { serverName } from "./NodeStatus";
import { inPanelTz } from "./tz";
import {
  Badge,
  Button,
  cn,
  Dropdown,
  DropdownDivider,
  DropdownItem,
  IconButton,
  IconChevron,
  IconDots,
  IconPencil,
  IconRestart,
  Modal,
  Section,
  Select,
  ShowMore,
  Switch,
  Textarea,
  TextInput,
  useConfirm,
} from "./ui";

// External subscriptions: servers that are not ours, read from another
// provider's subscription and handed on to users beside our own lanes. A section on
// the Servers page rather than a tab under Settings: to the operator these are
// servers — they sit in the same list a user sees — even though the panel owns
// nothing on them and the same access groups decide who gets which.

function fmtWhen(unix: number): string {
  return unix ? new Date(unix * 1000).toLocaleString(i18n.language, inPanelTz()) : "—";
}

// sourceKind names what a source is, since the stored value can be a URL or a
// pasted payload of any length.
function sourceKind(source: string): "url" | "happ" | "text" {
  const s = source.trim().toLowerCase();
  if (s.startsWith("https://") || s.startsWith("http://")) return "url";
  if (s.startsWith("happ://crypt")) return "happ";
  return "text";
}

export function ExternalServers() {
  const { t } = useTranslation();
  const [subs, setSubs] = useState<ExtSubscription[] | null>(null);
  const [servers, setServers] = useState<ExtServer[]>([]);
  const [nodes, setNodes] = useState<NodeView[]>([]);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<ExtSubscription | null>(null);
  const { busy, run, isBusy } = useAction();
  const { confirm, confirmNode } = useConfirm();

  const load = () =>
    getExternal()
      .then((r) => {
        setSubs(r.subscriptions ?? []);
        setServers(r.servers ?? []);
      })
      .catch((e) => notifyError(errMessage(e)));

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load();
    // The servers a subscription can be relayed through. Without them the choice
    // offers only "direct", which is still a working card.
    listNodes()
      .then((r) => setNodes(r.nodes ?? []))
      .catch(() => setNodes([]));
  }, []);

  const byServer = useMemo(() => {
    const m = new Map<number, ExtServer[]>();
    for (const s of servers) {
      const list = m.get(s.sub_id) ?? [];
      list.push(s);
      m.set(s.sub_id, list);
    }
    return m;
  }, [servers]);

  // Nothing imported and nothing being added: the section is its header and a button,
  // so a panel that never uses this pays no screen space for it.
  if (subs === null) return null;

  const sync = (s: ExtSubscription) =>
    run(
      async () => {
        const r = await syncExternal(s.id);
        notifySuccess(t("external.synced", { total: r.total, added: r.added, removed: r.removed }));
        await load();
      },
      { key: `sync-${s.id}` },
    );

  const remove = async (s: ExtSubscription) => {
    const ok = await confirm({
      title: t("external.deleteTitle"),
      body: t("external.deleteBody", { name: s.name }),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!ok) return;
    run(async () => {
      await deleteExternal(s.id);
      notifySuccess(t("external.deleted"));
      await load();
    });
  };

  return (
    <>
      {confirmNode}
      <Section
        title={t("external.title")}
        desc={t("external.hint")}
        action={
          <Button size="xs" variant="light" onClick={() => setAdding(true)}>
            {t("external.add")}
          </Button>
        }
      >
        {/* undefined, not false: the section draws no body at all for it, so a panel
            with no external subscriptions is a header and a button. */}
        {subs.length === 0 ? undefined : (
          <div className="flex flex-col gap-3">
            {subs.map((s) => (
              <ExtSubCard
                key={s.id}
                sub={s}
                servers={byServer.get(s.id) ?? []}
                nodes={nodes}
                busy={busy}
                syncing={isBusy(`sync-${s.id}`)}
                onSync={() => sync(s)}
                onEdit={() => setEditing(s)}
                onRemove={() => remove(s)}
                onEnabled={(v) =>
                  run(async () => {
                    await setExternalEnabled(s.id, v);
                    await load();
                  })
                }
                onRelay={(lane, serverId) =>
                  run(async () => {
                    await setExternalRelay(s.id, lane, serverId);
                    notifySuccess(t("external.deliverySaved"));
                    await load();
                  })
                }
                onToggle={(id, v) =>
                  run(async () => {
                    await setExternalServerEnabled(id, v);
                    await load();
                  })
                }
                onToggleAll={(v) =>
                  run(async () => {
                    await setExternalServersEnabled(s.id, v);
                    await load();
                  })
                }
              />
            ))}
          </div>
        )}
      </Section>

      {editing && (
        <EditExternalDialog
          sub={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void load();
          }}
        />
      )}

      {adding && (
        <AddExternalDialog
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            load();
          }}
        />
      )}
    </>
  );
}

// laneName is how a relay lane reads in the delivery choice.
const laneName = (lane: string) => (lane === "reality" ? "REALITY" : "TCP-TLS");

// relayValue packs a delivery choice into one select value: "" is direct, otherwise
// "<server id>:<lane>".
const relayValue = (lane: string, serverId: number) => (lane ? `${serverId}:${lane}` : "");

// ExtSubCard is one subscription, drawn like a server card on the same page: its state
// and name in the header with the actions as icons, then where its servers go and the
// servers themselves.
function ExtSubCard({
  sub,
  servers,
  nodes,
  busy,
  syncing,
  onSync,
  onEdit,
  onRemove,
  onEnabled,
  onRelay,
  onToggle,
  onToggleAll,
}: {
  sub: ExtSubscription;
  servers: ExtServer[];
  nodes: NodeView[];
  busy: boolean;
  syncing: boolean;
  onSync: () => void;
  onEdit: () => void;
  onRemove: () => void;
  onEnabled: (enabled: boolean) => void;
  onRelay: (lane: string, serverId: number) => void;
  onToggle: (id: number, enabled: boolean) => void;
  onToggleAll: (enabled: boolean) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const on = servers.filter((x) => x.enabled).length;
  const kind = sourceKind(sub.source);
  const dot = !sub.enabled ? "bg-gray-400" : sub.last_error ? "bg-warning" : "bg-success";

  // The delivery choice: direct, or a lane that runs on one of our servers. A relay
  // whose server or lane has gone stays listed, so the select still says what is set.
  const options = [{ value: "", label: t("external.direct") }];
  for (const n of nodes) {
    if (n.vless_enabled) {
      options.push({ value: relayValue("vless", n.id), label: t("external.via", { server: serverName(n), lane: laneName("vless") }) });
    }
    if (n.reality_enabled && n.reality_public_key) {
      options.push({ value: relayValue("reality", n.id), label: t("external.via", { server: serverName(n), lane: laneName("reality") }) });
    }
  }
  const current = relayValue(sub.relay_lane, sub.relay_server_id);
  if (current && !options.some((o) => o.value === current)) {
    options.push({ value: current, label: t("external.viaGone") });
  }
  const pick = (v: string) => {
    if (v === current) return;
    if (!v) return onRelay("", 0);
    const [id, lane] = v.split(":");
    onRelay(lane, Number(id));
  };

  return (
    <section className={cn("rounded-xl border border-brand-600/10 bg-white", !sub.enabled && "opacity-55")}>
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5 px-3.5 py-2.5">
        <span className={cn("size-2 shrink-0 rounded-full", dot)} />
        <span className="shrink truncate text-sm font-semibold text-ink">{sub.name}</span>
        <Badge size="xs" color="gray" className="shrink-0">
          {t(kind === "url" ? "external.kindUrl" : kind === "happ" ? "external.kindHapp" : "external.kindText")}
        </Badge>
        {!sub.enabled && (
          <Badge size="xs" color="gray" className="shrink-0">
            {t("conn.off")}
          </Badge>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <div className="w-52" title={t("external.delivery")}>
            <Select size="sm" value={current} onChange={pick} data={options} disabled={busy} className="w-full" />
          </div>
          <IconButton title={t("external.sync")} disabled={busy} onClick={onSync}>
            <IconRestart size={16} className={syncing ? "animate-spin" : undefined} />
          </IconButton>
          <IconButton title={t("common.edit")} disabled={busy} onClick={onEdit}>
            <IconPencil size={16} />
          </IconButton>
          <Dropdown
            align="end"
            width={220}
            trigger={
              <span
                title={t("external.manage")}
                className="inline-flex size-8 items-center justify-center rounded-lg text-gray-600 transition hover:bg-gray-100 active:scale-90"
              >
                <IconDots size={16} />
              </span>
            }
          >
            <DropdownItem onClick={() => onEnabled(!sub.enabled)}>
              {t(sub.enabled ? "external.turnOff" : "external.turnOn")}
            </DropdownItem>
            {sub.enabled && servers.length > 0 && (
              <>
                <DropdownItem onClick={() => onToggleAll(true)}>{t("external.enableAll")}</DropdownItem>
                <DropdownItem onClick={() => onToggleAll(false)}>{t("external.disableAll")}</DropdownItem>
              </>
            )}
            <DropdownDivider />
            <DropdownItem color="red" onClick={onRemove}>
              {t("common.delete")}
            </DropdownItem>
          </Dropdown>
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-brand-600/10 px-3.5 py-2">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="-ml-1 flex items-center gap-1.5 rounded-md px-1 py-0.5 text-sm text-ink transition hover:bg-gray-50"
        >
          <IconChevron size={14} className={cn("shrink-0 text-ink-muted transition", !open && "-rotate-90")} />
          <span className="font-medium">{t("external.servers")}</span>
          <span className="text-xs text-ink-muted">
            {sub.last_error && servers.length === 0 ? t("external.readFailed") : t("external.serversOf", { on, total: servers.length })}
          </span>
        </button>
        <span className="min-w-0 truncate text-xs text-ink-muted">
          {"· "}
          {kind === "url" ? <span className="font-mono">{sub.source}</span> : t("external.pastedSource")}
          {" · "}
          {t("external.lastRead", { when: fmtWhen(sub.last_fetch_at) })}
        </span>
        {sub.last_error && <span className="basis-full text-xs text-warning">{sub.last_error}</span>}
      </div>
      {open && <ServerList sub={sub} servers={servers} busy={busy} onToggle={onToggle} />}
    </section>
  );
}

// ServerList is one subscription's servers with their switches, paged like every
// other long list in the panel.
function ServerList({
  sub,
  servers,
  busy,
  onToggle,
}: {
  sub: ExtSubscription;
  servers: ExtServer[];
  busy: boolean;
  onToggle: (id: number, enabled: boolean) => void;
}) {
  const { t } = useTranslation();
  const rows = useShowMore(servers, { first: 10, step: 20, resetKey: servers });
  if (servers.length === 0) {
    return <p className="border-t border-brand-600/10 px-3.5 py-2.5 text-sm text-ink-muted">{t("external.noServers")}</p>;
  }
  return (
    <div className="border-t border-brand-600/10">
      <ul className="divide-y divide-brand-600/10">
        {rows.shown.map((x) => (
          <li key={x.id} className="flex items-center gap-3 px-3.5 py-2 text-sm">
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-0.5">
              <span className="truncate text-ink">{x.name}</span>
              <Badge color="gray" size="xs">
                {x.protocol}
              </Badge>
              <span className="truncate font-mono text-xs text-ink-muted">
                {x.host}:{x.port}
              </span>
            </div>
            <Switch checked={x.enabled} disabled={busy || !sub.enabled} onChange={(v) => onToggle(x.id, v)} />
          </li>
        ))}
      </ul>
      <ShowMore rest={rows.rest} onClick={rows.showMore} className="px-3.5 pb-2" />
    </div>
  );
}

// IdentityFields edits what the panel presents to a subscription that asks who is
// calling. Every field is an override: leaving one empty keeps the panel's default,
// which is what makes an ordinary subscription work without touching any of this.
// The placeholders show the actual default, so the operator can see what will be sent
// before deciding to replace it.
function IdentityFields({
  source,
  value,
  onChange,
}: {
  source: string;
  value: ExtIdentity;
  onChange: (v: ExtIdentity) => void;
}) {
  const { t } = useTranslation();
  const patch = (p: Partial<ExtIdentity>) => onChange({ ...value, ...p });
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-gray-200/70 bg-gray-50/60 p-3">
      <span className="text-sm font-medium text-ink">{t("external.identity")}</span>
      <p className="text-xs text-ink-muted">{t("external.identityHint")}</p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <TextInput
          label={t("external.hwid")}
          value={value.hwid}
          onChange={(v) => patch({ hwid: v })}
          placeholder={derivedHWIDPlaceholder(source)}
          mono
        />
        <TextInput
          label={t("external.userAgent")}
          value={value.user_agent}
          onChange={(v) => patch({ user_agent: v })}
          placeholder="rospanel/…"
        />
        <TextInput
          label={t("external.deviceOS")}
          value={value.device_os}
          onChange={(v) => patch({ device_os: v })}
          placeholder="rospanel"
        />
        <TextInput
          label={t("external.osVersion")}
          value={value.os_version}
          onChange={(v) => patch({ os_version: v })}
          placeholder={t("external.identityDefault")}
        />
        <TextInput
          label={t("external.deviceModel")}
          value={value.device_model}
          onChange={(v) => patch({ device_model: v })}
          placeholder="panel"
        />
      </div>
    </div>
  );
}

// derivedHWIDPlaceholder shows what the panel would send on its own. It is only a
// hint — the server derives the real value, and does so from the source as stored —
// so it is deliberately not computed here for a source that is not a URL, where no
// fetch happens and no device is ever presented.
function derivedHWIDPlaceholder(source: string): string {
  const s = source.trim();
  return /^https?:\/\//i.test(s) ? i18n.t("external.hwidAuto") : "—";
}

// EditExternalDialog changes where a subscription is read from and who the panel says
// it is, then re-reads it — so the answer to "did that fix it" is on screen rather than
// one manual sync away. Both fields together because changing the source usually means
// a different upstream, where the old device id means nothing.
function EditExternalDialog({
  sub,
  onClose,
  onSaved,
}: {
  sub: ExtSubscription;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(sub.name);
  const [source, setSource] = useState(sub.source);
  const [identity, setIdentity] = useState<ExtIdentity>({ ...EMPTY_EXT_IDENTITY, ...sub.identity });
  const { busy, run } = useAction();

  const submit = () =>
    run(async () => {
      const r = await updateExternalSource(sub.id, name.trim(), source.trim(), identity);
      notifySuccess(t("external.updated", { total: r.report.total }));
      onSaved();
    });

  return (
    <Modal open onClose={onClose} title={t("external.editTitle", { name: sub.name })}>
      <div className="flex flex-col gap-4">
        <TextInput label={t("external.name")} value={name} onChange={setName} placeholder={t("external.namePlaceholder")} />
        <Textarea label={t("external.source")} value={source} onChange={setSource} rows={4} />
        <IdentityFields source={source} value={identity} onChange={setIdentity} />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy} disabled={!source.trim()}>
            {t("common.save")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

// AddExternalDialog takes a source of any of the accepted shapes; the answer says
// how many servers it holds, so a wrong paste shows as zero right here.
function AddExternalDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [identity, setIdentity] = useState<ExtIdentity>(EMPTY_EXT_IDENTITY);
  const { busy, run } = useAction();

  const submit = () =>
    run(async () => {
      const r = await createExternal(name.trim(), source.trim(), identity);
      notifySuccess(t("external.added", { name: r.subscription.name, total: r.report.total }));
      onAdded();
    });

  return (
    <Modal open onClose={onClose} title={t("external.addTitle")}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-ink-muted">{t("external.addHint")}</p>
        <TextInput label={t("external.name")} value={name} onChange={setName} placeholder={t("external.namePlaceholder")} />
        <Textarea
          label={t("external.source")}
          value={source}
          onChange={setSource}
          rows={5}
          placeholder={"https://provider.example/sub/…\nhapp://crypt5/…\nvless://…"}
        />
        <IdentityFields source={source} value={identity} onChange={setIdentity} />
        <div className="flex justify-end gap-2">
          <Button variant="light" color="gray" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy} disabled={!source.trim()}>
            {t("external.import")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
