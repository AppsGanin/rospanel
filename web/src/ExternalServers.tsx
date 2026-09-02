import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createExternal,
  deleteExternal,
  getExternal,
  setExternalEnabled,
  setExternalServerEnabled,
  setExternalServersEnabled,
  syncExternal,
  type ExtServer,
  type ExtSubscription,
} from "./api";
import { useAction, useShowMore } from "./hooks";
import i18n from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  Card,
  Modal,
  ShowMore,
  Switch,
  Textarea,
  TextInput,
  useConfirm,
} from "./ui";

// External subscriptions: servers that are not ours, read from another
// provider's subscription and handed on to users beside our own lanes. A card on
// the Servers page rather than a tab under Settings: to the operator these are
// servers — they sit in the same list a user sees — even though the panel owns
// nothing on them and the same access groups decide who gets which.

function fmtWhen(unix: number): string {
  return unix ? new Date(unix * 1000).toLocaleString(i18n.language) : "—";
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
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const { busy, run, isBusy } = useAction();
  const { confirm, confirmNode } = useConfirm();

  const load = () =>
    getExternal()
      .then((r) => {
        setSubs(r.subscriptions ?? []);
        setServers(r.servers ?? []);
      })
      .catch((e) => notifyError(errMessage(e)));

  useEffect(() => {
    load();
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

  // Nothing imported and nothing being added: the card is one line with a button,
  // so a panel that never uses this pays no screen space for it.
  if (subs === null) return null;

  const toggleOpen = (id: number) =>
    setOpen((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

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
    <Card className="p-4">
      {confirmNode}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="font-bold text-ink">{t("external.title")}</h3>
          <p className="mt-0.5 text-sm text-ink-muted">{t("external.hint")}</p>
        </div>
        <Button variant="light" color="gray" onClick={() => setAdding(true)}>
          {t("external.add")}
        </Button>
      </div>

      {subs.length > 0 && (
        <div className="mt-4 flex flex-col gap-3">
          {subs.map((s) => {
            const list = byServer.get(s.id) ?? [];
            const on = list.filter((x) => x.enabled).length;
            const kind = sourceKind(s.source);
            return (
              <div key={s.id} className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-3">
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="truncate font-medium text-ink">{s.name}</span>
                    <Badge color="gray" size="xs">
                      {t(kind === "url" ? "external.kindUrl" : kind === "happ" ? "external.kindHapp" : "external.kindText")}
                    </Badge>
                    {!s.enabled && (
                      <Badge color="gray" size="xs">
                        {t("conn.off")}
                      </Badge>
                    )}
                    {s.last_error ? (
                      <Badge color="orange" size="xs" title={s.last_error}>
                        {t("external.readFailed")}
                      </Badge>
                    ) : (
                      <Badge color="gray" size="xs">
                        {t("external.serversOf", { on, total: list.length })}
                      </Badge>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    <Button
                      size="sm"
                      variant="light"
                      color="gray"
                      loading={isBusy(`sync-${s.id}`)}
                      disabled={busy}
                      onClick={() => sync(s)}
                    >
                      {t("external.sync")}
                    </Button>
                    <Button size="sm" variant="light" color="gray" disabled={busy} onClick={() => toggleOpen(s.id)}>
                      {t(open.has(s.id) ? "external.collapse" : "external.servers")}
                    </Button>
                    <Button size="sm" variant="light" color="red" disabled={busy} onClick={() => remove(s)}>
                      {t("common.delete")}
                    </Button>
                    <Switch
                      checked={s.enabled}
                      onChange={(v) =>
                        run(async () => {
                          await setExternalEnabled(s.id, v);
                          await load();
                        })
                      }
                    />
                  </div>
                </div>
                <p className="mt-1 text-xs text-ink-muted">
                  {kind === "url" ? (
                    <span className="break-all">{s.source}</span>
                  ) : (
                    t("external.pastedSource")
                  )}
                  {" · "}
                  {t("external.lastRead", { when: fmtWhen(s.last_fetch_at) })}
                  {s.last_error && (
                    <span className="block text-orange-600">{s.last_error}</span>
                  )}
                </p>
                {open.has(s.id) && (
                  <ServerList
                    sub={s}
                    servers={list}
                    busy={busy}
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
                )}
              </div>
            );
          })}
        </div>
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
    </Card>
  );
}

// ServerList is one subscription's servers with their switches, paged like every
// other long list in the panel.
function ServerList({
  sub,
  servers,
  busy,
  onToggle,
  onToggleAll,
}: {
  sub: ExtSubscription;
  servers: ExtServer[];
  busy: boolean;
  onToggle: (id: number, enabled: boolean) => void;
  onToggleAll: (enabled: boolean) => void;
}) {
  const { t } = useTranslation();
  const rows = useShowMore(servers, { first: 10, step: 20, resetKey: servers });
  if (servers.length === 0) {
    return <p className="mt-3 text-sm text-ink-muted">{t("external.noServers")}</p>;
  }
  return (
    <div className="mt-3 flex flex-col gap-1">
      <div className="mb-1 flex justify-end gap-2">
        <Button size="sm" variant="light" color="gray" disabled={busy} onClick={() => onToggleAll(true)}>
          {t("external.enableAll")}
        </Button>
        <Button size="sm" variant="light" color="gray" disabled={busy} onClick={() => onToggleAll(false)}>
          {t("external.disableAll")}
        </Button>
      </div>
      {rows.shown.map((x) => (
        <div
          key={x.id}
          className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-lg border border-gray-200/70 bg-white px-3 py-1.5 text-sm"
        >
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-ink">{x.name}</span>
            <Badge color="gray" size="xs">
              {x.protocol}
            </Badge>
            <span className="truncate font-mono text-xs text-ink-muted">
              {x.host}:{x.port}
            </span>
          </div>
          <Switch checked={x.enabled} disabled={busy || !sub.enabled} onChange={(v) => onToggle(x.id, v)} />
        </div>
      ))}
      <ShowMore rest={rows.rest} onClick={rows.showMore} className="mt-1" />
    </div>
  );
}

// AddExternalDialog takes a source of any of the accepted shapes; the answer says
// how many servers it holds, so a wrong paste shows as zero right here.
function AddExternalDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const { busy, run } = useAction();

  const submit = () =>
    run(async () => {
      const r = await createExternal(name.trim(), source.trim());
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
