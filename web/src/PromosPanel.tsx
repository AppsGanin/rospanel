import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  deletePromo,
  getPromoUses,
  listPromos,
  savePromo,
  type PromoCode,
  type PromoKind,
  type PromoUsage,
  type TariffPlan,
} from "./api";
import { dateToUnixEndOfDay, unixToLocalDate } from "./format";
import { useAction } from "./hooks";
import i18n, { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { useCan } from "./role";
import { useStepUpDialog } from "./stepup";
import { inPanelTz } from "./tz";
import {
  Button,
  Checkbox,
  cn,
  DatePicker,
  Drawer,
  EmptyState,
  IconButton,
  IconPencil,
  IconPlus,
  IconTrash,
  MICRO,
  Mono,
  Panel,
  ReadOnly,
  Select,
  SettingRow,
  Switch,
  TextInput,
  useConfirm,
  useWideBox,
} from "./ui";

const EMPTY_PROMO = (): PromoCode => ({
  id: 0,
  code: "",
  kind: "percent",
  value: 10,
  plan_ids: [],
  plan_id: 0,
  first_only: false,
  max_uses: 0,
  uses: 0,
  expires_at: 0,
  enabled: true,
  note: "",
  created_at: 0,
});

// A code people can read out and retype: no look-alike letters or digits.
const CODE_ALPHABET = "ABCDEFGHJKMNPQRSTUVWXYZ23456789";

function randomCode(): string {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => CODE_ALPHABET[b % CODE_ALPHABET.length]).join("");
}

const kinds = (): { value: PromoKind; label: string }[] => [
  { value: "percent", label: i18n.t("promo.kind.percent") },
  { value: "amount", label: i18n.t("promo.kind.amount") },
  { value: "days", label: i18n.t("promo.kind.days") },
  { value: "balance", label: i18n.t("promo.kind.balance") },
];

// promoEffect is the code's effect in a few characters: "−20%", "+7 d · Std".
function promoEffect(p: PromoCode, plans: TariffPlan[]): string {
  switch (p.kind) {
    case "percent":
      return `−${p.value}%`;
    case "amount":
      return `−${p.value.toLocaleString(currentLang())} ₽`;
    case "balance":
      return i18n.t("promo.onBalance", { sum: p.value.toLocaleString(currentLang()) });
    case "days": {
      const days = i18n.t("bill.nDays", { count: p.value });
      const plan = plans.find((x) => x.id === p.plan_id);
      return `+${days} · ${plan ? plan.name : i18n.t("promo.currentPlan")}`;
    }
  }
}

const TPL = "minmax(0,1.2fr) minmax(0,1.6fr) minmax(0,.7fr) minmax(0,.9fr) 76px";
const TPL_NARROW = "minmax(0,1fr) auto";
const WIDE_MIN = 620;

// PromosPanel is the promo code roster: a code the user enters in the bot or on the
// subscription page. Saved one at a time, beside the page's settings bar.
export function PromosPanel({ plans }: { plans: TariffPlan[] }) {
  const { t } = useTranslation();
  const canManage = useCan("billing.manage");
  const [list, setList] = useState<PromoCode[] | null>(null);
  const [err, setErr] = useState("");
  const [editor, setEditor] = useState<PromoCode | null>(null);
  const [expiry, setExpiry] = useState("");
  const [boxRef, wide] = useWideBox(WIDE_MIN);
  const { busy, run } = useAction();
  const { confirm, confirmNode } = useConfirm();
  const { ask, stepUpNode } = useStepUpDialog();
  const paid = plans.filter((p) => p.price_rub > 0);

  const reload = useCallback(() => {
    listPromos()
      .then((l) => {
        setList(l ?? []);
        setErr("");
      })
      .catch((e) => setErr(errMessage(e)));
  }, []);
  useEffect(reload, [reload]);

  const open = (p: PromoCode) => {
    setEditor({ ...p, plan_ids: [...(p.plan_ids ?? [])] });
    setExpiry(unixToLocalDate(p.expires_at));
  };

  const save = async () => {
    if (!editor) return;
    // A balance code hands out money, like a balance correction: same password.
    let password = "";
    if (editor.kind === "balance") {
      const creds = await ask({ title: t("promo.balanceConfirm"), confirmLabel: t("common.save") });
      if (!creds) return;
      password = creds.password;
    }
    run(async () => {
      await savePromo(
        {
          ...editor,
          code: editor.code.trim(),
          expires_at: expiry ? dateToUnixEndOfDay(expiry) : 0,
        },
        password,
      );
      setEditor(null);
      reload();
      notifySuccess(t("promo.saved"));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const remove = async (p: PromoCode) => {
    const ok = await confirm({
      title: t("promo.deleteTitle"),
      body: t("promo.deleteBody", { code: p.code }),
      confirmLabel: t("common.delete"),
      danger: true,
    });
    if (!ok) return;
    run(async () => {
      await deletePromo(p.id);
      reload();
      notifySuccess(t("promo.deleted"));
    }).catch((e) => notifyError(errMessage(e)));
  };

  const patch = (d: Partial<PromoCode>) => editor && setEditor({ ...editor, ...d });
  const discount = editor?.kind === "percent" || editor?.kind === "amount";
  const valueLabel =
    editor?.kind === "percent"
      ? t("promo.valuePercent")
      : editor?.kind === "days"
        ? t("promo.valueDays")
        : t("promo.valueRub");
  const now = Date.now() / 1000;

  return (
    <>
      {confirmNode}
      {stepUpNode}
      <ReadOnly when={!canManage}>
        <Panel
          title={t("promo.title")}
          aside={
            <IconButton
              variant="filled"
              color="brand"
              title={t("promo.new")}
              onClick={() => open({ ...EMPTY_PROMO(), code: randomCode() })}
            >
              <IconPlus />
            </IconButton>
          }
        >
          <SettingRow hint={t("promo.hint")} />
          {err ? (
            <SettingRow hint={<span className="text-danger">{err}</span>} />
          ) : !list ? null : list.length === 0 ? (
            <EmptyState title={t("promo.none")} />
          ) : (
            <div ref={boxRef}>
              {wide && (
                <div
                  className={cn(MICRO, "grid items-center gap-3 border-t border-gray-100 px-3.5 py-2")}
                  style={{ gridTemplateColumns: TPL }}
                >
                  <span className="truncate">{t("promo.colCode")}</span>
                  <span className="truncate">{t("promo.colEffect")}</span>
                  <span className="truncate">{t("promo.colUses")}</span>
                  <span className="truncate">{t("promo.colExpires")}</span>
                  <span />
                </div>
              )}
              {list.map((p) => {
                const over =
                  !p.enabled ||
                  (p.expires_at > 0 && p.expires_at <= now) ||
                  (p.max_uses > 0 && p.uses >= p.max_uses);
                const uses = p.max_uses > 0 ? `${p.uses} / ${p.max_uses}` : String(p.uses);
                const expires = p.expires_at
                  ? new Date(p.expires_at * 1000).toLocaleDateString(
                      currentLang(),
                      inPanelTz({ day: "2-digit", month: "2-digit", year: "numeric" }),
                    )
                  : "—";
                const actions = (
                  <span className="flex justify-end gap-0.5">
                    <IconButton title={t("common.edit")} nav onClick={() => open(p)}>
                      <IconPencil size={16} />
                    </IconButton>
                    <IconButton
                      color="red"
                      title={t("common.delete")}
                      disabled={busy}
                      onClick={() => remove(p)}
                    >
                      <IconTrash size={16} />
                    </IconButton>
                  </span>
                );
                return (
                  <div
                    key={p.id}
                    className={cn(
                      "grid items-center gap-x-3 gap-y-0.5 border-t border-gray-100 px-3.5 py-[7px]",
                      over && "opacity-60",
                    )}
                    style={{ gridTemplateColumns: wide ? TPL : TPL_NARROW }}
                  >
                    <Mono className="truncate text-[13px] font-medium text-ink" title={p.note}>
                      {p.code}
                    </Mono>
                    {wide ? (
                      <>
                        <span className="truncate text-xs text-ink-muted">
                          {promoEffect(p, plans)}
                          {p.first_only ? ` · ${t("promo.firstOnlyShort")}` : ""}
                        </span>
                        <Mono className="truncate text-xs text-ink-muted">{uses}</Mono>
                        <span className="truncate text-xs text-ink-muted">{expires}</span>
                        {actions}
                      </>
                    ) : (
                      <>
                        {actions}
                        <span className="col-span-2 truncate text-[11px] text-ink-muted">
                          {promoEffect(p, plans)} · {t("promo.usesN", { uses })} · {expires}
                        </span>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </Panel>
      </ReadOnly>

      <Drawer
        open={!!editor}
        onClose={() => setEditor(null)}
        title={editor?.id ? editor.code : t("promo.new")}
        footer={
          editor ? (
            <div className="flex justify-end gap-2">
              <Button variant="light" color="gray" size="sm" onClick={() => setEditor(null)}>
                {t("common.cancel")}
              </Button>
              <Button size="sm" onClick={save} loading={busy}>
                {t(editor.id ? "common.save" : "common.create")}
              </Button>
            </div>
          ) : undefined
        }
      >
        {editor && (
          <ReadOnly when={!canManage}>
            <div className="flex flex-col gap-4">
              <div className="flex items-end gap-2">
                <div className="min-w-0 flex-1">
                  <TextInput
                    label={t("promo.code")}
                    mono
                    value={editor.code}
                    onChange={(v) => patch({ code: v.replace(/\s/g, "") })}
                  />
                </div>
                <Button variant="light" color="gray" size="sm" onClick={() => patch({ code: randomCode() })}>
                  {t("promo.generate")}
                </Button>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <Select
                  label={t("promo.kindLabel")}
                  data={kinds()}
                  value={editor.kind}
                  onChange={(v) => patch({ kind: v as PromoKind })}
                />
                <TextInput
                  label={valueLabel}
                  type="number"
                  value={String(editor.value)}
                  onChange={(v) => patch({ value: Math.max(0, Math.floor(Number(v) || 0)) })}
                />
              </div>
              {editor.kind === "days" && (
                <Select
                  label={t("promo.grantPlan")}
                  data={[
                    { value: "0", label: t("promo.currentPlan") },
                    ...paid
                      .filter((p) => p.period_days > 0)
                      .map((p) => ({ value: String(p.id), label: p.name })),
                  ]}
                  value={String(editor.plan_id)}
                  onChange={(v) => patch({ plan_id: Number(v) })}
                />
              )}
              {discount && (
                <div className="flex flex-col gap-2">
                  <span className="text-sm font-medium text-ink">{t("promo.plans")}</span>
                  <div className="flex max-h-44 flex-col gap-1.5 overflow-y-auto rounded-lg border border-gray-200/80 bg-white/50 p-2">
                    {paid.map((p) => (
                      <Checkbox
                        key={p.id}
                        checked={editor.plan_ids.includes(p.id)}
                        onChange={(c) =>
                          patch({
                            plan_ids: c
                              ? [...editor.plan_ids, p.id]
                              : editor.plan_ids.filter((id) => id !== p.id),
                          })
                        }
                        label={p.name}
                      />
                    ))}
                  </div>
                  <p className="text-xs text-ink-muted">{t("promo.plansHint")}</p>
                  <SettingRow
                    inset
                    label={t("promo.firstOnly")}
                    control={
                      <Switch checked={editor.first_only} onChange={(v) => patch({ first_only: v })} />
                    }
                  />
                </div>
              )}
              <div className="grid gap-3 sm:grid-cols-2">
                <TextInput
                  label={t("promo.maxUses")}
                  type="number"
                  placeholder={t("common.unlimited")}
                  value={editor.max_uses ? String(editor.max_uses) : ""}
                  onChange={(v) => patch({ max_uses: Math.max(0, Math.floor(Number(v) || 0)) })}
                />
                <DatePicker label={t("promo.expires")} value={expiry} onChange={setExpiry} clearable />
              </div>
              {editor.id > 0 && <PromoUsesBlock id={editor.id} />}
              <TextInput label={t("promo.note")} value={editor.note} onChange={(v) => patch({ note: v })} />
              <SettingRow
                inset
                label={t("promo.enabled")}
                control={<Switch checked={editor.enabled} onChange={(v) => patch({ enabled: v })} />}
              />
            </div>
          </ReadOnly>
        )}
      </Drawer>
    </>
  );
}

// PromoUsesBlock lists who used a code and what money the orders it discounted brought.
function PromoUsesBlock({ id }: { id: number }) {
  const { t } = useTranslation();
  const [usage, setUsage] = useState<PromoUsage | null>(null);
  useEffect(() => {
    getPromoUses(id)
      .then(setUsage)
      .catch(() => setUsage(null));
  }, [id]);
  if (!usage) return null;
  return (
    <div className="flex flex-col gap-1.5 border-t border-gray-100 pt-3">
      <span className="text-sm font-medium text-ink">
        {t("promo.usedN", { count: usage.uses.length })}
        {usage.orders > 0 &&
          ` · ${t("promo.revenue", { count: usage.orders, sum: usage.revenue_rub.toLocaleString(currentLang()) })}`}
      </span>
      {usage.uses.length > 0 && (
        <div className="flex max-h-56 flex-col overflow-y-auto rounded-lg border border-gray-200/80">
          {usage.uses.map((u) => (
            <div
              key={`${u.user_id}-${u.used_at}`}
              className="flex items-center justify-between gap-3 border-t border-gray-100 px-2.5 py-1.5 text-xs first:border-t-0"
            >
              <span className="min-w-0 truncate text-ink">{u.name || `#${u.user_id}`}</span>
              <span className="shrink-0 text-ink-muted">
                {u.order_id ? `#${u.order_id} · ${u.amount_rub} ₽ · −${u.discount_rub} ₽ · ` : ""}
                {new Date(u.used_at * 1000).toLocaleDateString(
                  currentLang(),
                  inPanelTz({ day: "2-digit", month: "2-digit", year: "numeric" }),
                )}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
