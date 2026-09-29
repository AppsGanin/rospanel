import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n, { currentLang, td } from "./i18n";
import { type AutoRule, deleteAutoRule, listAutoRules, saveAutoRule, testBroadcast } from "./api";
import { HtmlEditor } from "./HtmlEditor";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Badge,
  Button,
  EmptyState,
  IconButton,
  IconClose,
  MICRO,
  Modal,
  Mono,
  Panel,
  Select,
  Switch,
  TextInput,
  cn,
  rowKey,
  useConfirm,
} from "./ui";

const BUTTONS_MAX = 3;

const blank = (trigger: string): AutoRule => ({
  id: 0,
  name: "",
  enabled: true,
  trigger,
  delay_hours: 24,
  text: "",
  buttons: [],
  discount_percent: 0,
  discount_days: 7,
});

// delayLabel says a delay in days when it is whole days, in hours otherwise.
function delayLabel(h: number): string {
  return h % 24 === 0 ? i18n.t("rules.inDays", { count: h / 24 }) : i18n.t("rules.inHours", { count: h });
}

// AutoRules is the list of automatic messages and their editor.
export function AutoRules() {
  const { t } = useTranslation();
  const { confirm, confirmNode } = useConfirm();
  const [rules, setRules] = useState<AutoRule[] | null>(null);
  const [triggers, setTriggers] = useState<string[]>([]);
  const [edit, setEdit] = useState<AutoRule | null>(null);
  const [buttons, setButtons] = useState<{ text: string; url: string; key: string }[]>([]);
  const [unit, setUnit] = useState<"h" | "d">("d");
  const [busy, setBusy] = useState(false);

  const load = () =>
    listAutoRules()
      .then((r) => {
        setRules(r.rules);
        setTriggers(r.triggers);
      })
      .catch((e) => notifyError(errMessage(e)));
  useEffect(() => {
    load();
  }, []);

  const open = (r: AutoRule) => {
    setEdit({ ...r });
    setButtons(r.buttons.map((b) => ({ ...b, key: rowKey() })));
    setUnit(r.delay_hours % 24 === 0 ? "d" : "h");
  };
  const draft = (): AutoRule | null =>
    edit && { ...edit, buttons: buttons.map(({ text, url }) => ({ text, url })) };

  const save = async (r: AutoRule) => {
    setBusy(true);
    try {
      await saveAutoRule(r);
      setEdit(null);
      notifySuccess(t("rules.saved"));
      await load();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const test = async () => {
    const r = draft();
    if (!r) return;
    try {
      await testBroadcast({ text: r.text, audience: "all", buttons: r.buttons }, null);
      notifySuccess(t("bc.testSent"));
    } catch (e) {
      notifyError(errMessage(e));
    }
  };
  const remove = async (r: AutoRule) => {
    const ok = await confirm({
      title: t("rules.deleteTitle"),
      body: t("rules.deleteBody", { name: r.name }),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!ok) return;
    try {
      await deleteAutoRule(r.id);
      await load();
    } catch (e) {
      notifyError(errMessage(e));
    }
  };

  if (!rules) return null;
  const delayValue = edit ? (unit === "d" ? edit.delay_hours / 24 : edit.delay_hours) : 0;

  return (
    <Panel
      title={t("rules.title")}
      aside={
        <Button size="xs" onClick={() => open(blank(triggers[0] ?? "no_connect"))}>
          {t("rules.add")}
        </Button>
      }
    >
      <p className="border-t border-gray-100 px-3.5 py-2 text-[11px] text-ink-muted">{t("rules.hint")}</p>
      {rules.length === 0 ? (
        <EmptyState title={t("rules.empty")} />
      ) : (
        rules.map((r) => (
          <div
            key={r.id}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-gray-100 px-3.5 py-2"
          >
            <Switch
              checked={r.enabled}
              disabled={busy}
              onChange={(v) => save({ ...r, enabled: v })}
            />
            <button type="button" className="min-w-0 flex-1 text-left" onClick={() => open(r)}>
              <span className="block truncate text-[13px] font-medium text-ink">{r.name}</span>
              <span className="block truncate text-[11px] text-ink-muted">
                {td(`rules.trigger.${r.trigger}`)} · {delayLabel(r.delay_hours)}
              </span>
            </button>
            {r.discount_percent > 0 && (
              <Badge size="xs" color="teal">
                −{r.discount_percent}%
              </Badge>
            )}
            {r.stats && (
              <Mono className="shrink-0 text-[11px] text-ink-muted">
                {t(r.trigger === "no_signup" ? "rules.statsSignup" : "rules.stats", {
                  sent: r.stats.sent,
                  converted: r.stats.converted,
                  revenue: r.stats.revenue_rub.toLocaleString(currentLang()),
                })}
              </Mono>
            )}
            <IconButton color="red" title={t("common.delete")} onClick={() => remove(r)}>
              <IconClose size={16} />
            </IconButton>
          </div>
        ))
      )}

      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        size="lg"
        title={edit?.id ? t("rules.editTitle") : t("rules.newTitle")}
        footer={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button variant="outline" color="gray" size="sm" onClick={test} disabled={!edit?.text.trim()}>
              {t("bc.sendTest")}
            </Button>
            <Button size="sm" loading={busy} onClick={() => draft() && save(draft() as AutoRule)}>
              {t("common.save")}
            </Button>
          </div>
        }
      >
        {edit && (
          <div className="flex flex-col gap-3">
            <TextInput
              label={t("rules.name")}
              value={edit.name}
              onChange={(v) => setEdit({ ...edit, name: v })}
            />
            <div>
              <Select
                label={t("rules.when")}
                data={triggers.map((k) => ({ value: k, label: td(`rules.trigger.${k}`) }))}
                value={edit.trigger}
                onChange={(v) =>
                  setEdit({ ...edit, trigger: v, discount_percent: v === "no_signup" ? 0 : edit.discount_percent })
                }
              />
              <p className="mt-1 text-[11px] text-ink-muted">{td(`rules.triggerDesc.${edit.trigger}`)}</p>
            </div>
            <div className="flex items-end gap-2">
              <div className="w-32">
                <TextInput
                  type="number"
                  label={t("rules.after")}
                  value={String(delayValue)}
                  onChange={(v) =>
                    setEdit({ ...edit, delay_hours: Math.max(1, Math.round(Number(v) || 1)) * (unit === "d" ? 24 : 1) })
                  }
                />
              </div>
              <div className="w-32">
                <Select
                  data={[
                    { value: "h", label: t("rules.hours") },
                    { value: "d", label: t("rules.days") },
                  ]}
                  value={unit}
                  onChange={(v) => {
                    const u = v as "h" | "d";
                    setUnit(u);
                    setEdit({ ...edit, delay_hours: Math.max(1, delayValue) * (u === "d" ? 24 : 1) });
                  }}
                />
              </div>
            </div>
            <div>
              <HtmlEditor value={edit.text} onChange={(v) => setEdit({ ...edit, text: v })} rows={5} />
              <p className="mt-1 text-[11px] text-ink-muted">{t("rules.placeholders")}</p>
            </div>
            <div className="flex flex-col gap-2">
              <p className={MICRO}>{t("bc.buttons")}</p>
              {buttons.map((b) => (
                <div key={b.key} className="flex items-end gap-2">
                  <div className="flex-1">
                    <TextInput
                      value={b.text}
                      placeholder={t("bc.buttonPlaceholder")}
                      onChange={(v) => setButtons((c) => c.map((x) => (x.key === b.key ? { ...x, text: v } : x)))}
                    />
                  </div>
                  <div className="flex-1">
                    <TextInput
                      value={b.url}
                      placeholder="https://example.com"
                      onChange={(v) => setButtons((c) => c.map((x) => (x.key === b.key ? { ...x, url: v } : x)))}
                    />
                  </div>
                  <IconButton
                    title={t("bc.removeButton")}
                    onClick={() => setButtons((c) => c.filter((x) => x.key !== b.key))}
                  >
                    <IconClose size={18} />
                  </IconButton>
                </div>
              ))}
              {buttons.length < BUTTONS_MAX && (
                <div>
                  <Button
                    variant="light"
                    size="sm"
                    onClick={() => setButtons((c) => [...c, { text: "", url: "", key: rowKey() }])}
                  >
                    {t("bc.addButton")}
                  </Button>
                </div>
              )}
            </div>
            {edit.trigger !== "no_signup" && (
              <div className={cn("flex flex-wrap items-end gap-2")}>
                <div className="w-40">
                  <TextInput
                    type="number"
                    label={t("rules.discount")}
                    value={String(edit.discount_percent)}
                    onChange={(v) => setEdit({ ...edit, discount_percent: Math.round(Number(v) || 0) })}
                  />
                </div>
                {edit.discount_percent > 0 && (
                  <div className="w-40">
                    <TextInput
                      type="number"
                      label={t("rules.discountDays")}
                      value={String(edit.discount_days)}
                      onChange={(v) => setEdit({ ...edit, discount_days: Math.round(Number(v) || 1) })}
                    />
                  </div>
                )}
                <p className="w-full text-[11px] text-ink-muted">{t("rules.discountHint")}</p>
              </div>
            )}
          </div>
        )}
      </Modal>
      {confirmNode}
    </Panel>
  );
}
