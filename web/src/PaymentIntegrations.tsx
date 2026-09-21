import { useTranslation } from "react-i18next";
import { type PaymentProvider } from "./api";
import i18n, { td } from "./i18n";
import {
  CenterLoader,
  cn,
  Code,
  Panel,
  Select,
  SettingRow,
  Switch,
  Textarea,
  TextInput,
} from "./ui";

// ProviderDraft is one provider's editable state (mirrors PaymentField kinds:
// secrets/text as strings, bools as "1"/"").
export type ProviderDraft = { enabled: boolean; config: Record<string, string> };

// draftFromProvider seeds a provider's editable form from the server's view: field
// values for text/bool, and empty strings for secrets (which are write-only — the
// server only tells us whether one is set, never its value).
export function draftFromProvider(p: PaymentProvider): ProviderDraft {
  const config: Record<string, string> = {};
  for (const f of p.fields) {
    if (f.kind === "secret") config[f.key] = "";
    else if (f.kind === "bool") config[f.key] = f.value === true ? "1" : "";
    else config[f.key] = typeof f.value === "string" ? f.value : "";
  }
  return { enabled: p.enabled, config };
}

// providerDirty reports whether a draft differs from the server's saved view.
export function providerDirty(p: PaymentProvider, draft: ProviderDraft): boolean {
  if (draft.enabled !== p.enabled) return true;
  return p.fields.some((f) => {
    if (f.kind === "secret") return draft.config[f.key] !== "";
    if (f.kind === "bool")
      return draft.config[f.key] !== (f.value === true ? "1" : "");
    return draft.config[f.key] !== (typeof f.value === "string" ? f.value : "");
  });
}

// ProviderCard is one provider's settings form, rendered entirely from the schema
// the server sends. It's controlled — edits bubble up via onChange and are saved by
// the page's shared bottom SaveBar, not here.
function ProviderCard({
  provider,
  draft,
  onChange,
}: {
  provider: PaymentProvider;
  draft: ProviderDraft;
  onChange: (d: ProviderDraft) => void;
}) {
  const { t } = useTranslation();
  const setField = (key: string, value: string) =>
    onChange({ ...draft, config: { ...draft.config, [key]: value } });

  // A provider is "configured" when every required field has a value: text fields
  // non-empty, secrets either already stored or being entered now.
  const configured = provider.fields.every((f) => {
    if (f.optional || f.kind === "bool") return true;
    if (f.kind === "secret") return f.is_set || draft.config[f.key] !== "";
    return (draft.config[f.key] ?? "") !== "";
  });

  const status = !draft.enabled
    ? { label: i18n.t("bill.provOff"), color: "gray" as const }
    : configured
      ? { label: i18n.t("bill.provOn"), color: "green" as const }
      : { label: i18n.t("bill.provUnset"), color: "orange" as const };

  return (
    <SettingRow
      label={
        <span className="flex flex-wrap items-baseline gap-x-2">
          {provider.label}
          <span
            className={cn(
              "text-[11px] font-normal",
              status.color === "green"
                ? "text-success"
                : status.color === "orange"
                  ? "text-warning"
                  : "text-ink-muted",
            )}
          >
            {status.label}
          </span>
        </span>
      }
      hint={td(provider.note)}
      control={
        <Switch
          checked={draft.enabled}
          onChange={(v) => onChange({ ...draft, enabled: v })}
        />
      }
    >
      {/* Credentials, revealed when the provider is on. */}
      {draft.enabled && (
        <div className="grid gap-2.5 sm:grid-cols-2">
          {provider.fields.map((f) => {
            if (f.kind === "bool") {
              return (
                <label
                  key={f.key}
                  className="flex items-center gap-2 text-xs text-ink"
                >
                  <Switch
                    checked={draft.config[f.key] === "1"}
                    onChange={(v) => setField(f.key, v ? "1" : "")}
                  />
                  {td(f.label)}
                  {f.help && (
                    <span className="text-[11px] text-ink-muted">— {td(f.help)}</span>
                  )}
                </label>
              );
            }
            if (f.kind === "select") {
              const opts = f.options ?? [];
              return (
                <div key={f.key}>
                  <Select
                    label={td(f.label)}
                    data={opts.map((o) => ({ ...o, label: td(o.label) }))}
                    value={draft.config[f.key] || opts[0]?.value || ""}
                    onChange={(v) => setField(f.key, v)}
                  />
                  {f.help && (
                    <p className="mt-1 text-[11px] text-ink-muted">{td(f.help)}</p>
                  )}
                </div>
              );
            }
            const isSecret = f.kind === "secret";
            // Every operator-visible string a provider describes itself with — label,
            // note, help, placeholder — is a dictionary key. td() falls back to the
            // string itself, so a brand name in the same field renders unchanged.
            const name = td(f.label);
            const label = isSecret && f.is_set ? t("bill.fieldSet", { label: name }) : name;
            return (
              <div key={f.key}>
                <TextInput
                  label={label}
                  value={draft.config[f.key] ?? ""}
                  onChange={(v) => setField(f.key, v)}
                  placeholder={isSecret && f.is_set ? "••••••••" : td(f.placeholder ?? "")}
                />
                {f.help && !isSecret && (
                  <p className="mt-1 text-[11px] text-ink-muted">{td(f.help)}</p>
                )}
              </div>
            );
          })}

          {provider.webhook_url && (
            <div className="sm:col-span-2">
              <p className="mb-1 text-[11px] text-ink-muted">{t("bill.webhookUrl")}</p>
              <Code block copy>
                {provider.webhook_url}
              </Code>
            </div>
          )}
        </div>
      )}
    </SettingRow>
  );
}

// ManualCard is manual payment in the shape of a provider: a switch, and the
// operator's own details revealed when it is on. First in the list because it is the
// method that needs no account anywhere.
function ManualCard({
  enabled,
  label,
  note,
  onEnabled,
  onLabel,
  onNote,
}: {
  enabled: boolean;
  label: string;
  note: string;
  onEnabled: (v: boolean) => void;
  onLabel: (v: string) => void;
  onNote: (v: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <SettingRow
      label={
        <span className="flex flex-wrap items-baseline gap-x-2">
          {t("bill.manualMethod")}
          <span
            className={cn(
              "text-[11px] font-normal",
              enabled ? "text-success" : "text-ink-muted",
            )}
          >
            {enabled ? t("bill.provOn") : t("bill.provOff")}
          </span>
        </span>
      }
      hint={t("bill.manualHint")}
      control={<Switch checked={enabled} onChange={onEnabled} />}
    >
      {enabled && (
        <div className="flex flex-col gap-2.5">
          <TextInput
            label={t("payField.displayName")}
            value={label}
            onChange={onLabel}
            placeholder={t("bill.manualMethod")}
          />
          <Textarea
            label={t("bill.manualDetails")}
            value={note}
            onChange={onNote}
            placeholder={t("bill.manualPlaceholder")}
            rows={4}
          />
        </div>
      )}
    </SettingRow>
  );
}

// PaymentIntegrations lists every payment method: manual payment, then every
// provider the panel knows about with its settings form. It's controlled by
// BillingPanel so edits ride the page's single bottom SaveBar. Providers, fields and
// validation all come from the server, so a newly added provider shows up here with
// no frontend change.
export function PaymentIntegrations({
  providers,
  drafts,
  err,
  onChange,
  manual,
  label,
  note,
  onManual,
  onLabel,
  onNote,
}: {
  providers: PaymentProvider[] | null;
  drafts: Record<string, ProviderDraft>;
  err: string;
  onChange: (key: string, d: ProviderDraft) => void;
  manual: boolean;
  label: string;
  note: string;
  onManual: (v: boolean) => void;
  onLabel: (v: string) => void;
  onNote: (v: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Panel title={t("bill.acceptTitle")}>
      <SettingRow hint={t("bill.acceptDescription")} />
      <ManualCard
        enabled={manual}
        label={label}
        note={note}
        onEnabled={onManual}
        onLabel={onLabel}
        onNote={onNote}
      />
      {err ? (
        <SettingRow hint={<span className="text-danger">{err}</span>} />
      ) : !providers ? (
        <CenterLoader />
      ) : (
        providers.map((p) => (
          <ProviderCard
            key={p.key}
            provider={p}
            draft={drafts[p.key] ?? draftFromProvider(p)}
            onChange={(d) => onChange(p.key, d)}
          />
        ))
      )}
    </Panel>
  );
}
