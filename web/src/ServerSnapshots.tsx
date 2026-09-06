import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type ConfigSnapshot,
  createConfigSnapshot,
  deleteConfigSnapshot,
  getConfigSnapshots,
  rollbackConfigSnapshot,
} from "./api";
import { currentLang } from "./i18n";
import { useAction } from "./hooks";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Section } from "./RoutingEditor";
import { Button, TextInput, useConfirm } from "./ui";

// ServerSnapshots is the master's config save-points: capture the whole server config
// (protocols, ports, REALITY, routing, egress, DNS, decoy, inbounds) and roll back to
// one if a change broke something. The certificate/domain identity is deliberately not
// part of a rollback — see the manager — so restoring never risks the live cert.
export function ServerSnapshots({ onRolledBack }: { onRolledBack?: () => void }) {
  const { t } = useTranslation();
  const [snaps, setSnaps] = useState<ConfigSnapshot[] | null>(null);
  const [label, setLabel] = useState("");
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();

  // On the first load a failure shows the empty state; on a later reload (after an action)
  // a transient GET blip keeps the list we already have rather than flashing "no snapshots".
  const reload = () =>
    getConfigSnapshots()
      .then(setSnaps)
      .catch(() => setSnaps((prev) => prev ?? []));
  useEffect(() => {
    reload();
  }, []);

  const stamp = (sec: number) => new Date(sec * 1000).toLocaleString(currentLang());

  return (
    <Section title={t("snapshot.title")} desc={t("snapshot.hint")}>
      <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-end">
        <div className="flex-1">
          <TextInput
            label={t("snapshot.label")}
            value={label}
            onChange={setLabel}
            placeholder={t("snapshot.labelPlaceholder")}
          />
        </div>
        <Button
          onClick={() =>
            run(async () => {
              await createConfigSnapshot(label.trim());
              setLabel("");
              await reload();
              notifySuccess(t("snapshot.saved"));
            })
          }
          disabled={busy}
        >
          {t("snapshot.save")}
        </Button>
      </div>

      {snaps === null ? (
        <p className="text-sm text-ink-muted">{t("common.loading")}</p>
      ) : snaps.length === 0 ? (
        <p className="text-sm text-ink-muted">{t("snapshot.empty")}</p>
      ) : (
        <div className="flex flex-col gap-1">
          {snaps.map((sn) => (
            <div
              key={sn.id}
              className="flex flex-wrap items-center gap-2 rounded-lg border border-gray-200/70 bg-gray-50/60 px-3 py-2 text-sm"
            >
              <span className="text-ink-muted">{stamp(sn.created_at)}</span>
              <span className="font-medium text-ink">
                {sn.label || (sn.auto ? t("snapshot.auto") : t("snapshot.manual"))}
              </span>
              <span className="ml-auto flex gap-3">
                <button
                  type="button"
                  className="text-accent hover:underline disabled:opacity-50"
                  disabled={busy}
                  onClick={async () => {
                    const ok = await confirm({
                      title: t("snapshot.rollbackTitle"),
                      body: t("snapshot.rollbackBody"),
                      confirmLabel: t("snapshot.rollback"),
                      danger: true,
                    });
                    if (!ok) return;
                    run(async () => {
                      try {
                        await rollbackConfigSnapshot(sn.id);
                        await reload();
                        notifySuccess(t("snapshot.rolledBack"));
                        // The rollback replaced the whole server config, so the sibling
                        // settings tabs still hold pre-rollback values as their save
                        // baseline — hand back to the parent to refresh/close rather than
                        // let a later Save silently re-persist the superseded config.
                        onRolledBack?.();
                      } catch (e) {
                        notifyError(errMessage(e));
                      }
                    });
                  }}
                >
                  {t("snapshot.rollback")}
                </button>
                <button
                  type="button"
                  className="text-red-500 hover:underline disabled:opacity-50"
                  disabled={busy}
                  onClick={async () => {
                    const ok = await confirm({
                      body: t("snapshot.deleteConfirm"),
                      confirmLabel: t("common.delete"),
                      danger: true,
                    });
                    if (!ok) return;
                    run(async () => {
                      await deleteConfigSnapshot(sn.id);
                      await reload();
                    });
                  }}
                >
                  {t("common.delete")}
                </button>
              </span>
            </div>
          ))}
        </div>
      )}
      {confirmNode}
    </Section>
  );
}
