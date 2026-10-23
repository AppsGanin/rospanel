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
  setExternalServerEnabled,
  setExternalServerName,
  setExternalServersEnabled,
  syncExternal,
  updateExternalSource,
} from "./api";
import { ExternalDeliveryDialog } from "./ExternalDelivery";
import { useAction, useShowMore } from "./hooks";
import i18n, { locale } from "./i18n";
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
  return unix ? new Date(unix * 1000).toLocaleString(locale(), inPanelTz()) : "—";
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
  const [delivering, setDelivering] = useState<ExtSubscription | null>(null);
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
                onDelivery={() => setDelivering(s)}
                onRenamed={() => void load()}
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

      {delivering && (
        <ExternalDeliveryDialog
          sub={delivering}
          servers={byServer.get(delivering.id) ?? []}
          nodes={nodes}
          onClose={() => setDelivering(null)}
          onSaved={() => {
            setDelivering(null);
            void load();
          }}
        />
      )}

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
  onDelivery,
  onToggle,
  onToggleAll,
  onRenamed,
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
  onDelivery: () => void;
  onToggle: (id: number, enabled: boolean) => void;
  onToggleAll: (enabled: boolean) => void;
  onRenamed: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const on = servers.filter((x) => x.enabled).length;
  const kind = sourceKind(sub.source);
  const dot = !sub.enabled ? "bg-gray-400" : sub.last_error ? "bg-warning" : "bg-success";

  // Where the servers go, in one line: as they are, or through how many lanes of ours.
  const relays = sub.relays ?? [];
  const relayServer = relays.length ? nodes.find((n) => n.id === relays[0].server_id) : undefined;
  const delivery =
    relays.length === 0
      ? t("external.direct")
      : relays.length === 1
        ? t("external.via", {
            server: relayServer ? serverName(relayServer) : `#${relays[0].server_id}`,
            lane: relays[0].lane === "reality" ? "REALITY" : "TCP-TLS",
          })
        : t("extRoute.viaCount", { n: relays.length });

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
        <span className="ms-auto flex shrink-0 items-center gap-1">
          <Button size="xs" variant="light" color="gray" title={t("external.delivery")} disabled={busy} onClick={onDelivery}>
            <span className="max-w-52 truncate">{delivery}</span>
          </Button>
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
          className="-ms-1 flex items-center gap-1.5 rounded-md px-1 py-0.5 text-sm text-ink transition hover:bg-gray-50"
        >
          <IconChevron size={14} className={cn("shrink-0 text-ink-muted transition", !open && "-rotate-90 rtl:rotate-90")} />
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
      {open && <ServerList sub={sub} servers={servers} busy={busy} onToggle={onToggle} onRenamed={onRenamed} />}
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
  onRenamed,
}: {
  sub: ExtSubscription;
  servers: ExtServer[];
  busy: boolean;
  onToggle: (id: number, enabled: boolean) => void;
  onRenamed: () => void;
}) {
  const { t } = useTranslation();
  const rows = useShowMore(servers, { first: 10, step: 20, resetKey: servers });
  const [renaming, setRenaming] = useState<ExtServer | null>(null);
  const [name, setName] = useState("");
  const { busy: saving, run } = useAction();
  const saveName = () => {
    if (!renaming) return;
    const id = renaming.id;
    run(async () => {
      await setExternalServerName(id, name.trim());
      setRenaming(null);
      notifySuccess(t("external.renamed"));
      onRenamed();
    }).catch((e) => notifyError(errMessage(e)));
  };
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
              <IconButton
                title={t("external.rename")}
                disabled={busy}
                onClick={() => {
                  setName(x.custom_name ?? "");
                  setRenaming(x);
                }}
              >
                <IconPencil size={13} />
              </IconButton>
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
      <Modal
        open={!!renaming}
        onClose={() => setRenaming(null)}
        title={t("external.renameTitle")}
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="light" color="gray" onClick={() => setRenaming(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={saveName} loading={saving}>
              {t("common.save")}
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-2">
          <TextInput
            label={t("external.renameLabel")}
            value={name}
            onChange={setName}
            placeholder={renaming?.source_name ?? ""}
            autoFocus
          />
          <p className="text-xs text-ink-muted">{t("external.renameHint", { name: renaming?.source_name ?? "" })}</p>
        </div>
      </Modal>
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
        {/* Only a source the panel fetches is asked who is calling: a pasted list or
            WireGuard configs are read in place. */}
        {sourceKind(source) === "url" && <IdentityFields source={source} value={identity} onChange={setIdentity} />}
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
          placeholder={"https://provider.example/sub/…\nhapp://crypt5/…\nvless://…\n[Interface] … (WireGuard .conf)"}
        />
        {/* Only a source the panel fetches is asked who is calling: a pasted list or
            WireGuard configs are read in place. */}
        {sourceKind(source) === "url" && <IdentityFields source={source} value={identity} onChange={setIdentity} />}
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
