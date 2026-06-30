import { useEffect, useRef, useState } from "react";
import {
  deleteBrandingLogo,
  saveBranding,
  uploadBrandingLogo,
  type ThemeColors,
} from "./api";
import { useBrand } from "./brand";
import { useAction } from "./hooks";
import { notifySuccess } from "./notify";
import { Button, SettingCard, TextInput } from "./ui";

// Curated accent swatches; the accent also drives the whole brand-* ramp.
const ACCENT_PRESETS = [
  "#0d4cd3", "#4f46e5", "#7c3aed", "#0891b2", "#0d9488",
  "#059669", "#dc2626", "#ea580c", "#e11d48", "#475569",
];

type ColorKey = keyof ThemeColors;

const COLOR_FIELDS: Array<{ key: ColorKey; label: string; hint: string }> = [
  { key: "accent", label: "Акцент", hint: "Кнопки, ссылки, активные вкладки, логотип" },
  { key: "text", label: "Текст", hint: "Основной текст и заголовки" },
  { key: "muted", label: "Приглушённый текст", hint: "Подписи, второстепенный текст" },
  { key: "bg", label: "Фон страницы", hint: "Подложка панели и страницы подписки" },
  { key: "surface", label: "Поверхность", hint: "Карточки, поля ввода, модалки" },
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
  const isDefault = value.toLowerCase() === def.toLowerCase();
  return (
    <div className="flex items-center gap-3">
      <input
        type="color"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={label}
        className="h-9 w-11 shrink-0 cursor-pointer rounded border border-gray-300 bg-white p-0.5"
      />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-ink">{label}</p>
        <p className="truncate text-xs text-ink-muted">{hint}</p>
      </div>
      <input
        value={value}
        onChange={(e) => {
          const h = normHex(e.target.value);
          onChange(h || e.target.value);
        }}
        spellCheck={false}
        className="w-24 rounded-lg border border-gray-300 bg-white px-2 py-1.5 text-sm font-mono uppercase text-ink outline-none focus:border-brand-500"
      />
      {!isDefault && (
        <button
          type="button"
          onClick={() => onChange(def)}
          className="text-xs text-ink-muted underline-offset-2 hover:text-accent hover:underline"
        >
          сброс
        </button>
      )}
    </div>
  );
}

export function BrandingSettings() {
  const brand = useBrand();
  const [name, setName] = useState("");
  const [theme, setTheme] = useState<ThemeColors>(brand.default_theme);
  const [init, setInit] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const { isBusy, run } = useAction();

  // Seed local fields from the loaded branding once.
  useEffect(() => {
    if (brand.loaded && !init) {
      setName(brand.panel_name === brand.default_name ? "" : brand.panel_name);
      setTheme(brand.theme);
      setInit(true);
    }
  }, [brand.loaded, brand.panel_name, brand.theme, brand.default_name, init]);

  const setColor = (key: ColorKey, v: string) =>
    setTheme((t) => ({ ...t, [key]: v }));

  const resetAll = () => setTheme(brand.default_theme);

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
        await saveBranding(name.trim(), clean);
        await brand.refresh();
        notifySuccess("Брендинг сохранён");
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
        notifySuccess("Логотип загружен");
      },
      { key: "logo" },
    );
  };
  const removeLogo = () =>
    run(
      async () => {
        await deleteBrandingLogo();
        await brand.refresh();
        notifySuccess("Логотип сброшен на стандартный");
      },
      { key: "logo" },
    );

  return (
    <SettingCard
      title="Брендинг"
      description="Название, цвета и логотип панели. Применяется и на странице подписки."
    >
      <div className="flex flex-col gap-4">
        <TextInput
          label="Название панели"
          placeholder={brand.default_name}
          value={name}
          onChange={setName}
        />

        <div>
          <div className="mb-2 flex items-center justify-between">
            <p className="text-sm font-medium text-ink">Цвета</p>
            <button
              type="button"
              onClick={resetAll}
              className="text-xs text-ink-muted underline-offset-2 hover:text-accent hover:underline"
            >
              Сбросить все
            </button>
          </div>

          <div className="mb-3 flex flex-wrap items-center gap-2">
            {ACCENT_PRESETS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setColor("accent", c)}
                title={c}
                aria-label={`Акцент ${c}`}
                className={
                  "h-7 w-7 rounded-full border transition " +
                  (theme.accent.toLowerCase() === c.toLowerCase()
                    ? "border-white ring-2 ring-brand-600 ring-offset-2"
                    : "border-gray-300 hover:scale-110")
                }
                style={{ background: c }}
              />
            ))}
          </div>

          <div className="flex flex-col gap-3">
            {COLOR_FIELDS.map((f) => (
              <ColorField
                key={f.key}
                label={f.label}
                hint={f.hint}
                value={theme[f.key]}
                def={brand.default_theme[f.key]}
                onChange={(v) => setColor(f.key, v)}
              />
            ))}
          </div>
        </div>

        <div>
          <p className="mb-1.5 text-sm font-medium text-ink">Логотип</p>
          <div className="flex items-center gap-3">
            {brand.has_custom_logo && (
              <img
                src={brand.logoURL}
                alt=""
                className="h-12 w-12 rounded-lg border border-gray-300 bg-white object-contain p-1"
              />
            )}
            <Button
              variant="light"
              color="gray"
              loading={isBusy("logo")}
              onClick={onPickLogo}
            >
              Загрузить логотип
            </Button>
            {brand.has_custom_logo && (
              <Button
                variant="subtle"
                color="red"
                loading={isBusy("logo")}
                onClick={removeLogo}
              >
                Сбросить
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
          <p className="mt-1.5 text-xs text-ink-muted">
            PNG или JPEG, до 512 КБ, не больше 1024×1024 px.
          </p>
        </div>

        <div>
          <Button loading={isBusy("brand")} onClick={save}>
            Сохранить
          </Button>
        </div>
      </div>
    </SettingCard>
  );
}
