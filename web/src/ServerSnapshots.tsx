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
import {
  Button,
  IconButton,
  IconRestart,
  IconTrash,
  Mono,
  Section,
  SettingRow,
  TextInput,
  useConfirm,
} from "./ui";

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

  const rollback = (sn: ConfigSnapshot) => async () => {
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
        // The rollback replaced the whole server config, so the sibling settings tabs
        // still hold pre-rollback values as their save baseline — hand back to the
        // parent to refresh/close rather than let a later Save silently re-persist the
        // superseded config.
        onRolledBack?.();
      } catch (e) {
        notifyError(errMessage(e));
      }
    });
  };

  const remove = (sn: ConfigSnapshot) => async () => {
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
  };

  return (
    <Section
      title={t("snapshot.title")}
      desc={t("snapshot.hint")}
      action={
        <Button
          size="xs"
          variant="light"
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
      }
      flush
    >
      <SettingRow
        label={t("snapshot.label")}
        wideField
        field={
          <TextInput
            value={label}
            onChange={setLabel}
            placeholder={t("snapshot.labelPlaceholder")}
          />
        }
      />

      {snaps === null ? (
        <SettingRow hint={t("common.loading")} />
      ) : snaps.length === 0 ? (
        <SettingRow hint={t("snapshot.empty")} />
      ) : (
        snaps.map((sn) => (
          <div
            key={sn.id}
            className="flex items-center gap-3 border-t border-gray-100 px-3.5 py-[7px]"
          >
            <Mono className="shrink-0 text-[11px] text-ink-muted">
              {stamp(sn.created_at)}
            </Mono>
            <span className="min-w-0 flex-1 truncate text-xs font-medium text-ink">
              {sn.label || (sn.auto ? t("snapshot.auto") : t("snapshot.manual"))}
            </span>
            <IconButton
              title={t("snapshot.rollback")}
              disabled={busy}
              onClick={rollback(sn)}
            >
              <IconRestart />
            </IconButton>
            <IconButton
              color="red"
              title={t("common.delete")}
              disabled={busy}
              onClick={remove(sn)}
            >
              <IconTrash />
            </IconButton>
          </div>
        ))
      )}
      {confirmNode}
    </Section>
  );
}
