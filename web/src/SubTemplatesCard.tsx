import { useTranslation } from "react-i18next";
import type { SubTemplates } from "./api";
import { Panel, SettingRow, Textarea } from "./ui";

// The placeholders each format's template must carry, kept next to the field that
// needs them. Mirrored from internal/sub/template.go — the server validates on save,
// so a mismatch here shows up as a refusal with a message, not as a broken profile.
const SLOTS: Record<keyof SubTemplates, string[]> = {
  clash: ["proxies: # LEAVE THIS LINE!", "    # LEAVE THIS LINE!"],
  singbox: ["{{proxies}}", "{{tags}}", "{{group}}"],
  xray: ["{{outbounds}}", "{{remarks}}"],
};

// SubTemplatesCard edits the operator's own profile documents. Empty means the
// generated profile, which is what an operator who does not want this never touches.
export function SubTemplatesCard({
  value,
  onChange,
}: {
  value: SubTemplates;
  onChange: (v: SubTemplates) => void;
}) {
  const { t } = useTranslation();
  const field = (key: keyof SubTemplates) => (
    <SettingRow
      key={key}
      label={t(`subTpl.${key}` as "subTpl.clash")}
      hint={
        <span className="flex flex-wrap items-center gap-1">
          {t("subTpl.slots")}
          {SLOTS[key].map((slot) => (
            <code
              key={slot}
              className="rounded border border-gray-200 bg-white px-1.5 py-0.5 font-mono text-[11px] text-ink-muted"
            >
              {slot}
            </code>
          ))}
        </span>
      }
    >
      <Textarea
        rows={6}
        value={value[key]}
        placeholder={t("subTpl.placeholder")}
        onChange={(v) => onChange({ ...value, [key]: v })}
      />
    </SettingRow>
  );
  return (
    <Panel title={t("subTpl.title")}>
      <SettingRow hint={t("subTpl.hint")} />
      {(["clash", "singbox", "xray"] as const).map(field)}
    </Panel>
  );
}
