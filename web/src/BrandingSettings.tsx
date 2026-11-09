import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  deleteBrandingLogo,
  saveBranding,
  uploadBrandingLogo,
  type ThemeColors,
} from "./api";
import { useBrand } from "./brand";
import { useLegalDocs } from "./LegalDocs";
import { useAction } from "./hooks";
import { notifySuccess } from "./notify";
import {
  useReadOnly,
  Button,
  IconButton,
  IconRestart,
  Panel,
  SaveBar,
  SegmentedControl,
  SettingRow,
  TextInput,
} from "./ui";

// Curated accent swatches; the accent also drives the whole brand-* ramp.
const ACCENT_PRESETS = [
  "#0d4cd3", "#4f46e5", "#7c3aed", "#0891b2", "#0d9488",
  "#059669", "#dc2626", "#ea580c", "#e11d48", "#475569",
];

type ColorKey = keyof ThemeColors;

const COLOR_FIELDS: Array<{ key: ColorKey; label: string; hint: string }> = [
  { key: "accent", label: "brand.accent", hint: "brand.accentHint" },
  { key: "text", label: "brand.text", hint: "brand.textHint" },
  { key: "muted", label: "brand.muted", hint: "brand.mutedHint" },
  { key: "bg", label: "brand.bg", hint: "brand.bgHint" },
  { key: "surface", label: "brand.surface", hint: "brand.surfaceHint" },
];

function normHex(v: string): string {
  return /^#[0-9a-fA-F]{6}$/.test(v.trim()) ? v.trim().toLowerCase() : "";
}

function ColorField({
  label,
  hint,
  value,
  def,
  onChange,
}: {
  label: string;
  hint: string;
  value: string;
  def: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const isDefault = value.toLowerCase() === def.toLowerCase();
  const ro = useReadOnly();
  return (
    <SettingRow
      label={label}
      hint={hint}
      field={
        <div className="flex items-center gap-2">
          <input
            type="color"
            disabled={ro}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            aria-label={label}
            className="h-7 w-9 shrink-0 cursor-pointer rounded border border-gray-300 bg-white p-0.5"
          />
          <div className="min-w-0 flex-1">
            <TextInput
              value={value}
              mono
              className="uppercase"
              onChange={(v) => onChange(normHex(v) || v)}
            />
          </div>
          {/* Back to the shipped colour — shown only when this one has been changed,
              so the row is quiet until there is something to undo. */}
          <IconButton
            title={t("brand.reset")}
            disabled={isDefault}
            onClick={() => onChange(def)}
          >
            <IconRestart size={14} />
          </IconButton>
        </div>
      }
    />
  );
}

export function BrandingSettings() {
  const { t } = useTranslation();
  const ro = useReadOnly();
  const brand = useBrand();
  const [name, setName] = useState("");
  const [savedName, setSavedName] = useState("");
  const [theme, setTheme] = useState<ThemeColors>(brand.default_theme);
  const [savedTheme, setSavedTheme] = useState<ThemeColors>(brand.default_theme);
  // The dark theme's accent is "" while it follows the light one.
  const [dark, setDark] = useState<ThemeColors>(brand.default_theme_dark);
  const [savedDark, setSavedDark] = useState<ThemeColors>(brand.default_theme_dark);
  // Which of the two themes the colour rows edit.
  const [editing, setEditing] = useState<"light" | "dark">("light");
  const [init, setInit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const { isBusy, run } = useAction();
  const legal = useLegalDocs();

  // Seed local fields from the loaded branding once.
  useEffect(() => {
    if (brand.loaded && !init) {
      const nm = brand.panel_name === brand.default_name ? "" : brand.panel_name;
      setName(nm);
      setSavedName(nm);
      setTheme(brand.theme);
      setSavedTheme(brand.theme);
      const d = {
        ...brand.theme_dark,
        accent:
          brand.theme_dark.accent.toLowerCase() === brand.theme.accent.toLowerCase()
            ? ""
            : brand.theme_dark.accent,
      };
      setDark(d);
      setSavedDark(d);
      setInit(true);
    }
  }, [brand.loaded, brand.panel_name, brand.theme, brand.theme_dark, brand.default_name, init]);

  const isDark = editing === "dark";
  // What the rows show and reset to for the theme being edited.
  const shown: ThemeColors = isDark ? { ...dark, accent: dark.accent || theme.accent } : theme;
  const defaults: ThemeColors = isDark
    ? { ...brand.default_theme_dark, accent: theme.accent }
    : brand.default_theme;

  const setColor = (key: ColorKey, v: string) => {
    if (!isDark) return setTheme((t) => ({ ...t, [key]: v }));
    // An accent equal to the light one goes back to following it.
    const val = key === "accent" && v.toLowerCase() === theme.accent.toLowerCase() ? "" : v;
    setDark((t) => ({ ...t, [key]: val }));
  };

  const resetAll = () =>
    isDark ? setDark(brand.default_theme_dark) : setTheme(brand.default_theme);

  const brandDirty =
    name !== savedName ||
    JSON.stringify(theme) !== JSON.stringify(savedTheme) ||
    JSON.stringify(dark) !== JSON.stringify(savedDark);
  const dirty = brandDirty || legal.dirty;

  const cancel = () => {
    setName(savedName);
    setTheme(savedTheme);
    setDark(savedDark);
    legal.cancel();
  };

  const save = () =>
    run(
      async () => {
        // Only send valid #rrggbb; blanks/invalid fall back to defaults.
        const fix = (k: ColorKey) => normHex(theme[k]) || brand.default_theme[k];
        const clean: ThemeColors = {
          accent: fix("accent"),
          text: fix("text"),
          muted: fix("muted"),
          bg: fix("bg"),
          surface: fix("surface"),
        };
        // The dark accent goes out as the light one while it follows it; the
        // server stores a dark colour only when it differs from its default.
        const fixD = (k: ColorKey) => normHex(dark[k]) || brand.default_theme_dark[k];
        const cleanDark: ThemeColors = {
          accent: normHex(dark.accent) || clean.accent,
          text: fixD("text"),
          muted: fixD("muted"),
          bg: fixD("bg"),
          surface: fixD("surface"),
        };
        if (brandDirty) {
          await saveBranding(name.trim(), clean, cleanDark);
          await brand.refresh();
          setSavedName(name.trim());
          setSavedTheme(clean);
          const keptDark = { ...cleanDark, accent: dark.accent ? cleanDark.accent : "" };
          setDark(keptDark);
          setSavedDark(keptDark);
        }
        await legal.save();
        notifySuccess(t("brand.saved"));
      },
      { key: "brand" },
    );

  const onPickLogo = () => fileRef.current?.click();
  const onLogoFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    run(
      async () => {
        await uploadBrandingLogo(file);
        await brand.refresh();
        notifySuccess(t("brand.logoUploaded"));
      },
      { key: "logo" },
    );
  };
  const removeLogo = () =>
    run(
      async () => {
        await deleteBrandingLogo();
        await brand.refresh();
        notifySuccess(t("brand.logoReset"));
      },
      { key: "logo" },
    );

  return (
    <div className="flex flex-1 flex-col gap-3.5">
      <Panel title={t("settings.tabBranding")}>
        <SettingRow
          label={t("brand.panelName")}
          hint={t("brand.description")}
          field={
            <TextInput
              placeholder={brand.default_name}
              value={name}
              onChange={setName}
            />
          }
        />
        <SettingRow
          label={t("brand.logo")}
          hint={t("brand.logoHint")}
          control={
            <div className="flex items-center gap-2">
              {brand.has_custom_logo && (
                <img
                  src={brand.logoURL}
                  alt=""
                  className="size-8 rounded border border-gray-300 bg-white object-contain p-0.5"
                />
              )}
              <Button
                size="xs"
                variant="light"
                color="gray"
                loading={isBusy("logo")}
                onClick={onPickLogo}
              >
                {t("brand.uploadLogo")}
              </Button>
              {brand.has_custom_logo && (
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  loading={isBusy("logo")}
                  onClick={removeLogo}
                >
                  {t("usersPanel.reset")}
                </Button>
              )}
              <input
                ref={fileRef}
                type="file"
                accept="image/png,image/jpeg"
                className="hidden"
                onChange={onLogoFile}
              />
            </div>
          }
        />
      </Panel>

      {/* Two themes of five colours each, and everything else in the panel
          derives from them. The presets are a shortcut to the one that drives
          the rest. */}
      <Panel
        title={t("brand.colors")}
        aside={
          <div className="flex items-center gap-3">
            <button
              type="button"
              disabled={ro}
              onClick={resetAll}
              className="text-[11px] text-ink-muted underline-offset-2 transition hover:text-accent hover:underline"
            >
              {t("brand.resetAll")}
            </button>
            <SegmentedControl
              size="xs"
              nav
              value={editing}
              onChange={(v) => setEditing(v as "light" | "dark")}
              data={[
                { value: "light", label: t("brand.lightTheme") },
                { value: "dark", label: t("brand.darkTheme") },
              ]}
            />
          </div>
        }
      >
        <SettingRow>
          <div className="flex flex-wrap items-center gap-2">
            {ACCENT_PRESETS.map((c) => (
              <button
                key={c}
                type="button"
                disabled={ro}
                onClick={() => setColor("accent", c)}
                title={c}
                aria-label={t("brand.accentSwatch", { color: c })}
                className={
                  // 28px of colour; the ring says which one is picked.
                  "size-7 rounded-full border transition ring-offset-2 " +
                  (shown.accent.toLowerCase() === c.toLowerCase()
                    ? "border-white ring-2 ring-brand-600 ring-offset-2"
                    : "border-gray-300 hover:scale-110")
                }
                style={{ background: c }}
              />
            ))}
          </div>
        </SettingRow>
        {COLOR_FIELDS.map((f) => (
          <ColorField
            key={`${editing}-${f.key}`}
            label={t(f.label as "brand.accent")}
            hint={t(
              (isDark && f.key === "accent" ? "brand.darkAccentHint" : f.hint) as "brand.accentHint",
            )}
            value={shown[f.key]}
            def={defaults[f.key]}
            onChange={(v) => setColor(f.key, v)}
          />
        ))}
      </Panel>

      {legal.panel}

      <SaveBar
        dirty={dirty}
        busy={isBusy("brand")}
        onSave={save}
        onCancel={cancel}
      />
    </div>
  );
}
