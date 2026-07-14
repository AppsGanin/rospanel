// Tailwind UI primitives — a small in-house component kit replacing Mantine.
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/* ------------------------------------------------------------------ icons */
type IconProps = { size?: number; className?: string };
const svg = (size: number, className: string | undefined, d: ReactNode) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    className={className}
    aria-hidden
  >
    {d}
  </svg>
);
export const IconChevron = ({ size = 16, className }: IconProps) =>
  svg(size, className, <path d="M6 9l6 6 6-6" />);
export const IconClose = ({ size = 20, className }: IconProps) =>
  svg(size, className, <path d="M18 6 6 18M6 6l12 12" />);
export const IconBurger = ({ size = 22, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <path d="M3 6h18" />
      <path d="M3 12h18" />
      <path d="M3 18h18" />
    </>,
  );
export const IconCheck = ({ size = 16, className }: IconProps) =>
  svg(size, className, <path d="M20 6 9 17l-5-5" />);
export const IconPencil = ({ size = 16, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </>,
  );
export const IconCopy = ({ size = 16, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M5 15a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2" />
    </>,
  );
export const IconHeart = ({ size = 20, className }: IconProps) =>
  svg(
    size,
    className,
    <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78L12 21l8.84-8.61a5.5 5.5 0 0 0 0-7.78z" />,
  );
export const IconShield = ({ size = 20, className }: IconProps) =>
  svg(
    size,
    className,
    <path d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />,
  );
export const IconCalendar = ({ size = 18, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <rect x="3" y="4" width="18" height="17" rx="2" />
      <path d="M3 9h18M8 2v4M16 2v4" />
    </>,
  );
export const IconEye = ({ size = 18, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
      <circle cx="12" cy="12" r="3" />
    </>,
  );
export const IconEyeOff = ({ size = 18, className }: IconProps) =>
  svg(
    size,
    className,
    <>
      <path d="M9.9 5.2A10.5 10.5 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4M6.2 6.2A17 17 0 0 0 2 12s3.5 7 10 7a10.5 10.5 0 0 0 4.1-.8" />
      <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
      <path d="M3 3l18 18" />
    </>,
  );
// GitHub mark — a filled glyph, so it doesn't use the stroke-based `svg` helper.
export const IconGithub = ({ size = 20, className }: IconProps) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="currentColor"
    className={className}
    aria-hidden
  >
    <path d="M12 .5C5.37.5 0 5.87 0 12.5c0 5.3 3.438 9.8 8.205 11.387.6.113.82-.26.82-.577 0-.285-.01-1.04-.015-2.04-3.338.726-4.042-1.61-4.042-1.61-.546-1.387-1.333-1.756-1.333-1.756-1.09-.745.082-.73.082-.73 1.205.085 1.84 1.237 1.84 1.237 1.07 1.835 2.807 1.305 3.492.997.108-.776.42-1.305.762-1.605-2.665-.303-5.467-1.332-5.467-5.93 0-1.31.468-2.38 1.235-3.22-.123-.303-.535-1.523.117-3.176 0 0 1.008-.322 3.3 1.23a11.5 11.5 0 0 1 3.003-.404c1.02.005 2.047.138 3.006.404 2.29-1.552 3.297-1.23 3.297-1.23.653 1.653.24 2.873.118 3.176.77.84 1.233 1.91 1.233 3.22 0 4.61-2.806 5.624-5.48 5.92.43.372.823 1.102.823 2.222 0 1.604-.015 2.898-.015 3.293 0 .32.216.695.825.577C20.565 22.297 24 17.797 24 12.5 24 5.87 18.63.5 12 .5z" />
  </svg>
);

/* ----------------------------------------------------------------- spinner */
export function Spinner({ size = 16, className }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      className={cn("animate-spin", className)}
      aria-hidden
    >
      <circle
        cx="12"
        cy="12"
        r="9"
        stroke="currentColor"
        strokeWidth="3"
        opacity="0.25"
        fill="none"
      />
      <path
        d="M21 12a9 9 0 0 0-9-9"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
        fill="none"
      />
    </svg>
  );
}

// CenterLoader fills the content area with a centered spinner while a screen's
// initial data is still loading.
export function CenterLoader() {
  return (
    <div className="flex animate-fade-in justify-center py-20 text-accent">
      <Spinner size={34} />
    </div>
  );
}

// Skeleton is a pulsing placeholder block. Pass className to set size and shape.
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded bg-gray-200", className)} />;
}

/* ------------------------------------------------------------------ button */
type Color = "brand" | "red" | "teal" | "orange" | "gray";
type Variant = "filled" | "light" | "subtle" | "outline";
type Size = "xs" | "sm" | "md";

const BTN: Record<Variant, Record<Color, string>> = {
  filled: {
    brand: "bg-brand-600 text-onaccent hover:bg-brand-700",
    red: "bg-brandred-500 text-onaccent hover:bg-brandred-600",
    teal: "bg-emerald-600 text-onaccent hover:bg-emerald-700",
    orange: "bg-orange-500 text-onaccent hover:bg-orange-600",
    gray: "bg-gray-700 text-gray-50 hover:bg-gray-800",
  },
  light: {
    brand: "accent-tint text-accent accent-tint-hover",
    red: "danger-tint text-danger danger-tint-hover",
    teal: "success-tint text-success success-tint-hover",
    orange: "warning-tint text-warning warning-tint-hover",
    gray: "bg-gray-100 text-gray-700 hover:bg-gray-200",
  },
  subtle: {
    brand: "bg-transparent text-accent accent-tint-hover",
    red: "bg-transparent text-danger danger-tint-hover",
    teal: "bg-transparent text-success success-tint-hover",
    orange: "bg-transparent text-warning warning-tint-hover",
    gray: "bg-transparent text-gray-600 hover:bg-gray-100",
  },
  outline: {
    brand: "bg-white text-accent border border-accent accent-tint-hover",
    red: "bg-white text-danger border border-danger danger-tint-hover",
    teal: "bg-white text-success border border-success success-tint-hover",
    orange:
      "bg-white text-warning border border-warning warning-tint-hover",
    gray: "bg-white text-gray-800 border border-gray-300 hover:bg-gray-50",
  },
};
const SIZE: Record<Size, string> = {
  xs: "text-xs px-2.5 py-1.5",
  sm: "text-sm px-3 py-1.5",
  md: "text-sm px-4 py-2",
};

export function Button({
  children,
  variant = "filled",
  color = "brand",
  size = "md",
  loading,
  fullWidth,
  disabled,
  className,
  href,
  target,
  onClick,
  type = "button",
}: {
  children: ReactNode;
  variant?: Variant;
  color?: Color;
  size?: Size;
  loading?: boolean;
  fullWidth?: boolean;
  disabled?: boolean;
  className?: string;
  href?: string;
  target?: string;
  onClick?: () => void;
  type?: "button" | "submit";
}) {
  const cls = cn(
    "inline-flex items-center justify-center gap-2 rounded-lg font-semibold select-none",
    "transition duration-150 active:scale-[0.97] disabled:opacity-60 disabled:cursor-not-allowed disabled:active:scale-100",
    BTN[variant][color],
    SIZE[size],
    fullWidth && "w-full",
    className,
  );
  if (href) {
    return (
      <a className={cls} href={href} target={target} rel="noreferrer">
        {children}
      </a>
    );
  }
  return (
    <button
      className={cls}
      disabled={disabled || loading}
      onClick={onClick}
      type={type}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}

export function IconButton({
  children,
  onClick,
  color = "gray",
  disabled,
  className,
  title,
}: {
  children: ReactNode;
  onClick?: () => void;
  color?: Color;
  disabled?: boolean;
  className?: string;
  title?: string;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "inline-flex h-8 w-8 items-center justify-center rounded-lg transition active:scale-90",
        BTN.subtle[color],
        "disabled:opacity-40 disabled:cursor-not-allowed disabled:active:scale-100",
        className,
      )}
    >
      {children}
    </button>
  );
}

/* -------------------------------------------------------------------- card */
export function Card({
  children,
  className,
  onClick,
  style,
}: {
  children: ReactNode;
  className?: string;
  onClick?: () => void;
  style?: React.CSSProperties;
}) {
  return (
    <div
      onClick={onClick}
      style={style}
      className={cn(
        "rounded-2xl border border-brand-600/6 bg-white shadow-sm transition",
        "hover:shadow-lg flex flex-col",
        onClick && "cursor-pointer",
        className,
      )}
    >
      {children}
    </div>
  );
}

// SettingCard is a settings section: a padded Card with a bold title, an optional
// muted description, and an optional right-aligned action (a Switch/Button in the
// header). The body renders below the header.
export function SettingCard({
  title,
  description,
  action,
  stackAction,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  // stackAction drops the action below the text on phones instead of keeping it on
  // the title row. Off by default: most actions here are a Switch, which stays put
  // happily in a corner at any width. Turn it on for a real button, which a long
  // description would otherwise squeeze into a sliver on a narrow screen.
  stackAction?: boolean;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("p-4", className)}>
      <div
        className={cn(
          "mb-3 flex items-start justify-between gap-3",
          stackAction && "flex-col sm:flex-row",
        )}
      >
        <div className="min-w-0">
          <h3 className="font-bold text-ink">{title}</h3>
          {description && (
            <p className="mt-1 text-sm text-ink-muted">{description}</p>
          )}
        </div>
        {action}
      </div>
      {children}
    </Card>
  );
}

// SaveBar is the sticky bottom action bar shown while a page has unsaved edits.
// Leaving the page (it unmounts) discards the in-memory changes, which the hint
// makes explicit. Render it once per page; it returns null when not dirty.
export function SaveBar({
  dirty,
  busy,
  onSave,
  onCancel,
  saveDisabled,
}: {
  dirty: boolean;
  busy?: boolean;
  onSave: () => void;
  onCancel: () => void;
  saveDisabled?: boolean;
}) {
  if (!dirty) return null;
  return (
    <div className="fixed inset-x-0 bottom-0 z-50 border-t border-gray-200 bg-white/95 backdrop-blur">
      <div className="mx-auto flex max-w-6xl flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
        <div className="min-w-0">
          <p className="text-sm font-medium text-ink">Есть несохранённые изменения</p>
          <p className="text-xs text-ink-muted">
            При уходе со страницы изменения отменятся
          </p>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button
            variant="light"
            color="gray"
            onClick={onCancel}
            className="flex-1 sm:flex-none"
          >
            Отменить
          </Button>
          <Button
            loading={busy}
            disabled={saveDisabled}
            onClick={onSave}
            className="flex-1 sm:flex-none"
          >
            Сохранить
          </Button>
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------- inputs */
function Field({ label, children }: { label?: string; children: ReactNode }) {
  if (!label) return <>{children}</>;
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium text-ink">{label}</span>
      {children}
    </label>
  );
}

const inputCls =
  "w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-ink outline-none " +
  "placeholder:text-gray-400 focus:border-brand-500 focus:ring-2 focus:ring-brand-100";

export function TextInput({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  autoFocus,
  mono,
  disabled,
  className,
}: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  autoFocus?: boolean;
  mono?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <Field label={label}>
      <input
        className={cn(
          inputCls,
          mono && "font-mono",
          disabled && "cursor-not-allowed bg-gray-50 text-ink-muted",
          className,
        )}
        value={value}
        type={type}
        placeholder={placeholder}
        autoFocus={autoFocus}
        disabled={disabled}
        onChange={(e) => onChange(e.currentTarget.value)}
      />
    </Field>
  );
}

export function Textarea({
  label,
  value,
  onChange,
  placeholder,
  rows = 3,
  hint,
}: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
  hint?: string;
}) {
  return (
    <Field label={label}>
      <textarea
        className={cn(inputCls, "resize-y")}
        value={value}
        rows={rows}
        placeholder={placeholder}
        onChange={(e) => onChange(e.currentTarget.value)}
      />
      {hint && <p className="mt-1 text-xs text-ink-muted">{hint}</p>}
    </Field>
  );
}

export function PasswordInput(
  props: Omit<Parameters<typeof TextInput>[0], "type" | "mono">,
) {
  const [show, setShow] = useState(false);
  return (
    <Field label={props.label}>
      <div className="relative">
        <input
          className={cn(inputCls, "pr-10", props.className)}
          value={props.value}
          type={show ? "text" : "password"}
          placeholder={props.placeholder}
          autoFocus={props.autoFocus}
          onChange={(e) => props.onChange(e.currentTarget.value)}
        />
        <button
          type="button"
          onClick={() => setShow((s) => !s)}
          aria-label={show ? "Скрыть пароль" : "Показать пароль"}
          title={show ? "Скрыть пароль" : "Показать пароль"}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-500 transition hover:text-gray-700"
        >
          {show ? <IconEyeOff /> : <IconEye />}
        </button>
      </div>
    </Field>
  );
}

// AnchoredPopover renders children in a portal positioned under `anchor`, with a
// transparent full-screen catcher that closes on outside click / Escape / scroll.
function AnchoredPopover({
  anchor,
  onClose,
  children,
}: {
  anchor: HTMLElement | null
  onClose: () => void
  children: (rect: DOMRect) => ReactNode
}) {
  const [rect, setRect] = useState<DOMRect | null>(anchor ? anchor.getBoundingClientRect() : null)
  const panelRef = useRef<HTMLDivElement>(null)
  useEscape(onClose, !!anchor) // topmost-only Escape (see escapeStack)
  useEffect(() => {
    if (!anchor) return
    const update = () => setRect(anchor.getBoundingClientRect())
    update()
    const onScroll = (e: Event) => {
      // Scrolling inside the popover (e.g. a long option list) must not close it;
      // only page/ancestor scroll, which would detach the panel, does.
      if (panelRef.current && e.target instanceof Node && panelRef.current.contains(e.target)) {
        return
      }
      onClose()
    }
    window.addEventListener('resize', update)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [anchor, onClose])
  if (!rect) return null
  return createPortal(
    <div className="fixed inset-0 z-260" onMouseDown={onClose}>
      <div ref={panelRef} onMouseDown={(e) => e.stopPropagation()}>
        {children(rect)}
      </div>
    </div>,
    document.body,
  )
}

const triggerCls =
  'flex w-full items-center justify-between gap-2 rounded-lg border border-gray-300 bg-white px-3 py-2 text-left text-sm text-ink ' +
  'outline-none transition hover:border-gray-400 focus:border-brand-500 focus:ring-2 focus:ring-brand-100'

export function Select({
  label,
  value,
  onChange,
  data,
  searchable,
  placeholder = 'Выберите…',
  className,
}: {
  label?: string
  value: string
  onChange: (v: string) => void
  data: { value: string; label: string }[]
  searchable?: boolean
  placeholder?: string
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const ref = useRef<HTMLButtonElement>(null)
  const current = data.find((o) => o.value === value)
  const filtered = searchable && q ? data.filter((o) => o.label.toLowerCase().includes(q.toLowerCase())) : data

  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
    setQ('')
  }

  return (
    <Field label={label}>
      <button
        ref={ref}
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(triggerCls, className)}
      >
        <span className={cn('truncate', !current && 'text-gray-400')}>
          {current ? current.label : placeholder}
        </span>
        <IconChevron className="shrink-0 text-gray-400" />
      </button>
      {open && (
        <AnchoredPopover anchor={ref.current} onClose={() => setOpen(false)}>
          {(rect) => (
            <div
              className="animate-scale-in origin-top overflow-hidden rounded-xl border border-gray-200 bg-white shadow-lg"
              style={{ position: 'fixed', left: rect.left, top: rect.bottom + 4, width: rect.width }}
            >
              {searchable && (
                <div className="border-b border-gray-100 p-2">
                  <input
                    autoFocus
                    value={q}
                    onChange={(e) => setQ(e.currentTarget.value)}
                    placeholder="Поиск…"
                    className="w-full rounded-md border border-gray-200 px-2 py-1.5 text-sm outline-none focus:border-brand-400"
                  />
                </div>
              )}
              <div className="max-h-60 overflow-y-auto py-1">
                {filtered.length === 0 && (
                  <p className="px-3 py-2 text-sm text-gray-400">Ничего не найдено</p>
                )}
                {filtered.map((o) => (
                  <button
                    key={o.value}
                    type="button"
                    onClick={() => pick(o.value)}
                    className={cn(
                      'flex w-full items-center justify-between px-3 py-2 text-left text-sm transition hover:bg-gray-50',
                      o.value === value ? 'font-semibold text-accent' : 'text-ink',
                    )}
                  >
                    <span className="truncate">{o.label}</span>
                    {o.value === value && <IconCheck className="shrink-0 text-accent" />}
                  </button>
                ))}
              </div>
            </div>
          )}
        </AnchoredPopover>
      )}
    </Field>
  )
}

// TagsInput is a multi-value combobox: existing values render as removable chips,
// free text is added on Enter, and an optional preset list drops down from the
// chevron. Values are stored verbatim (callers pass raw Xray matchers); `options`
// only maps known values to friendlier labels and offers quick-add presets.
export function TagsInput({
  label,
  hint,
  value,
  onChange,
  options,
  placeholder = 'добавить и Enter…',
}: {
  label?: string
  hint?: string
  value: string[]
  onChange: (v: string[]) => void
  options?: { value: string; label: string }[]
  placeholder?: string
}) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState('')
  const [q, setQ] = useState('')
  const boxRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const add = (v: string) => {
    v = v.trim()
    if (v && !value.includes(v)) onChange([...value, v])
    setDraft('')
  }
  const remove = (v: string) => onChange(value.filter((x) => x !== v))
  const labelFor = (v: string) => options?.find((o) => o.value === v)?.label ?? v
  const avail = (options ?? []).filter((o) => !value.includes(o.value))
  const ql = q.trim().toLowerCase()
  const matched = ql
    ? avail.filter((o) => o.label.toLowerCase().includes(ql) || o.value.toLowerCase().includes(ql))
    : avail
  const SHOWN = 100
  const shown = matched.slice(0, SHOWN)
  const closePopover = () => {
    setOpen(false)
    setQ('')
  }

  // NB: deliberately NOT wrapped in <Field>'s <label>. A <label> implicitly
  // associates with its first labelable descendant — here the first chip's
  // remove <button> — so clicking anywhere in the label (tag text, the field
  // title, empty space) would forward the click to that button and delete the
  // first tag. Use a plain <div>; the box below handles click-to-focus itself.
  return (
    <div>
      {label && <span className="mb-1 block text-sm font-medium text-ink">{label}</span>}
      <div
        ref={boxRef}
        onClick={() => inputRef.current?.focus()}
        className="flex w-full cursor-text flex-wrap items-center gap-1.5 rounded-lg border border-gray-300 bg-white px-2 py-1.5 text-sm transition focus-within:border-brand-500 focus-within:ring-2 focus-within:ring-brand-100"
      >
        {value.map((v) => (
          <span
            key={v}
            className="inline-flex max-w-full items-center gap-1 rounded-md bg-gray-100 py-0.5 pl-2 pr-1 text-xs font-medium text-ink"
          >
            <span className="min-w-0 truncate">{labelFor(v)}</span>
            <button
              type="button"
              aria-label="Удалить"
              title="Удалить"
              // preventDefault on mousedown so the click only removes (and doesn't
              // also fire the container's focus handler).
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.stopPropagation()
                remove(v)
              }}
              className="flex h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 transition hover:bg-gray-300 hover:text-gray-700"
            >
              <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <path d="M3 3l6 6M9 3l-6 6" />
              </svg>
            </button>
          </span>
        ))}
        <input
          ref={inputRef}
          value={draft}
          onChange={(e) => setDraft(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              add(draft)
            } else if (e.key === 'Backspace' && !draft && value.length) {
              remove(value[value.length - 1])
            }
          }}
          placeholder={value.length ? '' : placeholder}
          className="min-w-25 flex-1 bg-transparent py-0.5 outline-none placeholder:text-gray-400"
        />
        {avail.length > 0 && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              setOpen((o) => !o)
            }}
            className="shrink-0 text-gray-400 transition hover:text-gray-600"
          >
            <IconChevron />
          </button>
        )}
      </div>
      {hint && <p className="mt-1 text-xs text-ink-muted">{hint}</p>}
      {open && avail.length > 0 && (
        <AnchoredPopover anchor={boxRef.current} onClose={closePopover}>
          {(rect) => (
            <div
              className="animate-scale-in origin-top overflow-hidden rounded-xl border border-gray-200 bg-white shadow-lg"
              style={{ position: 'fixed', left: rect.left, top: rect.bottom + 4, width: rect.width }}
            >
              <div className="border-b border-gray-100 p-2">
                <input
                  autoFocus
                  value={q}
                  onChange={(e) => setQ(e.currentTarget.value)}
                  placeholder="Поиск категории…"
                  className="w-full rounded-md border border-gray-200 px-2 py-1.5 text-sm outline-none focus:border-brand-400"
                />
              </div>
              <div className="max-h-72 overflow-y-auto py-1">
                {shown.length === 0 && (
                  <p className="px-3 py-2 text-sm text-gray-400">Ничего не найдено</p>
                )}
                {shown.map((o) => (
                  <button
                    key={o.value}
                    type="button"
                    onClick={() => add(o.value)}
                    className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm text-ink transition hover:bg-gray-50"
                  >
                    <span className="truncate">{o.label}</span>
                    <span className="ml-2 shrink-0 font-mono text-xs text-gray-400">{o.value}</span>
                  </button>
                ))}
                {matched.length > SHOWN && (
                  <p className="px-3 py-2 text-xs text-gray-400">
                    Показано {SHOWN} из {matched.length} — уточните поиск
                  </p>
                )}
              </div>
            </div>
          )}
        </AnchoredPopover>
      )}
    </div>
  )
}

const MONTHS = [
  'Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь',
  'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь',
]
const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс']

function ymd(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
function parseYmd(s: string): Date | null {
  if (!s) return null
  const [y, m, d] = s.split('-').map(Number)
  if (!y || !m || !d) return null
  return new Date(y, m - 1, d)
}

// DatePicker holds a YYYY-MM-DD string (empty = unset) and renders a calendar
// popover. `min` (YYYY-MM-DD) disables earlier days.
export function DatePicker({
  label,
  value,
  onChange,
  min,
  placeholder = 'бессрочно',
}: {
  label?: string
  value: string
  onChange: (v: string) => void
  min?: string
  placeholder?: string
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLButtonElement>(null)
  const selected = parseYmd(value)
  const [view, setView] = useState<Date>(selected ?? new Date())
  const minDate = min ? parseYmd(min) : null

  const display = selected
    ? selected.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
    : ''

  // Build the 6-week grid for the viewed month (Monday-first).
  const first = new Date(view.getFullYear(), view.getMonth(), 1)
  const startOffset = (first.getDay() + 6) % 7 // Mon=0
  const cells: (Date | null)[] = []
  for (let i = 0; i < startOffset; i++) cells.push(null)
  const days = new Date(view.getFullYear(), view.getMonth() + 1, 0).getDate()
  for (let d = 1; d <= days; d++) cells.push(new Date(view.getFullYear(), view.getMonth(), d))

  const disabled = (d: Date) => {
    if (!minDate) return false
    const a = new Date(d.getFullYear(), d.getMonth(), d.getDate())
    const b = new Date(minDate.getFullYear(), minDate.getMonth(), minDate.getDate())
    return a < b
  }

  return (
    <Field label={label}>
      <button
        ref={ref}
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={triggerCls}
      >
        <span className={cn('truncate', !display && 'text-gray-400')}>{display || placeholder}</span>
        <IconCalendar className="shrink-0 text-gray-400" />
      </button>
      {open && (
        <AnchoredPopover anchor={ref.current} onClose={() => setOpen(false)}>
          {(rect) => (
            <div
              className="animate-scale-in origin-top rounded-xl border border-gray-200 bg-white p-3 shadow-lg"
              style={{ position: 'fixed', left: rect.left, top: rect.bottom + 4, width: 260 }}
            >
              <div className="mb-2 flex items-center justify-between">
                <button
                  type="button"
                  onClick={() => setView(new Date(view.getFullYear(), view.getMonth() - 1, 1))}
                  className="rounded-md p-1 text-gray-500 hover:bg-gray-100"
                >
                  <IconChevron className="rotate-90" />
                </button>
                <span className="text-sm font-semibold text-ink">
                  {MONTHS[view.getMonth()]} {view.getFullYear()}
                </span>
                <button
                  type="button"
                  onClick={() => setView(new Date(view.getFullYear(), view.getMonth() + 1, 1))}
                  className="rounded-md p-1 text-gray-500 hover:bg-gray-100"
                >
                  <IconChevron className="-rotate-90" />
                </button>
              </div>
              <div className="mb-1 grid grid-cols-7 text-center text-[11px] font-medium text-gray-400">
                {WEEKDAYS.map((w) => (
                  <span key={w}>{w}</span>
                ))}
              </div>
              <div className="grid grid-cols-7 gap-0.5">
                {cells.map((d, i) =>
                  d === null ? (
                    <span key={i} />
                  ) : (
                    <button
                      key={i}
                      type="button"
                      disabled={disabled(d)}
                      onClick={() => {
                        onChange(ymd(d))
                        setOpen(false)
                      }}
                      className={cn(
                        'flex h-8 items-center justify-center rounded-md text-sm transition',
                        value === ymd(d)
                          ? 'bg-brand-600 font-semibold text-onaccent'
                          : 'text-ink accent-tint-hover',
                        'disabled:cursor-not-allowed disabled:text-gray-300 disabled:hover:bg-transparent',
                      )}
                    >
                      {d.getDate()}
                    </button>
                  ),
                )}
              </div>
              {value && (
                <button
                  type="button"
                  onClick={() => {
                    onChange('')
                    setOpen(false)
                  }}
                  className="mt-2 w-full rounded-md py-1.5 text-sm font-medium text-danger danger-tint-hover"
                >
                  Очистить
                </button>
              )}
            </div>
          )}
        </AnchoredPopover>
      )}
    </Field>
  )
}

/* ------------------------------------------------------------------ switch */
export function Switch({
  checked,
  onChange,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition disabled:opacity-50",
        checked ? "bg-brand-600" : "bg-gray-300",
      )}
    >
      <span
        className={cn(
          "inline-block h-5 w-5 transform rounded-full bg-onaccent shadow transition",
          checked ? "translate-x-5" : "translate-x-0.5",
        )}
      />
    </button>
  );
}

// ToggleRow is a labelled switch row: label (+ optional hint) on the left, a
// Switch on the right — the shared form for an on/off setting.
export function ToggleRow({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div>
        <p className="text-sm font-medium text-ink">{label}</p>
        {hint && <p className="text-xs text-ink-muted">{hint}</p>}
      </div>
      <Switch checked={checked} onChange={onChange} />
    </div>
  );
}

/* ----------------------------------------------------------------- checkbox */
// Checkbox is a card-style selectable row: a custom check box + label, the whole
// row clickable and highlighted when checked.
export function Checkbox({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer select-none items-center gap-3 rounded-xl border px-3 py-2.5 text-sm transition",
        checked
          ? "border-accent accent-tint"
          : "border-gray-200 bg-white hover:border-gray-300 hover:bg-gray-50",
      )}
    >
      <input
        type="checkbox"
        className="sr-only"
        checked={checked}
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
      <span
        className={cn(
          "flex h-5 w-5 shrink-0 items-center justify-center rounded-md border transition",
          checked ? "border-brand-600 bg-brand-600 text-onaccent" : "border-gray-300 bg-white",
        )}
      >
        {checked && <IconCheck size={14} />}
      </span>
      <span className="min-w-0 flex-1">
        <span className={cn("block", checked ? "font-semibold text-ink" : "text-ink")}>{label}</span>
        {hint && <span className="block text-xs text-ink-muted">{hint}</span>}
      </span>
    </label>
  );
}

/* ------------------------------------------------------------------- badge */
const BADGE: Record<string, string> = {
  brand: "accent-tint text-accent",
  red: "danger-tint text-danger",
  teal: "success-tint text-success",
  green: "success-tint text-success",
  orange: "warning-tint text-warning",
  gray: "bg-gray-100 text-gray-600",
  greenSolid: "bg-emerald-500 text-onaccent",
};
export function Badge({
  children,
  color = "brand",
  size = "sm",
  className,
}: {
  children: ReactNode;
  color?: keyof typeof BADGE;
  size?: "xs" | "sm";
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-md font-medium whitespace-nowrap",
        size === "xs" ? "px-1.5 py-0.5 text-xs" : "px-2 py-0.5 text-sm",
        // BADGE is a Record<string, string>, so `keyof` is just string and a colour
        // that isn't in the palette type-checks fine — then renders as bare text with
        // no background. Fall back to the accent instead of vanishing.
        BADGE[color] ?? BADGE.brand,
        className,
      )}
    >
      {children}
    </span>
  );
}

/* ----------------------------------------------------------------- divider */
export function Divider({ label }: { label?: string }) {
  if (!label) return <hr className="border-gray-200" />;
  return (
    <div className="flex items-center gap-3 text-sm font-medium text-ink-muted">
      <span className="whitespace-nowrap">{label}</span>
      <hr className="grow border-gray-200" />
    </div>
  );
}

/* -------------------------------------------------------------------- code */
export function Code({
  children,
  block,
  copy,
  className,
}: {
  children: ReactNode;
  block?: boolean;
  copy?: boolean; // show a copy button inside (block only); copies the text content
  className?: string;
}) {
  const { copied, copy: doCopy } = useCopy();
  const base =
    "rounded-md bg-gray-100 font-mono text-xs text-ink " +
    (block ? "block whitespace-pre-wrap break-all p-3" : "px-1.5 py-0.5");
  if (copy && block) {
    return (
      <div className="relative">
        <code className={cn(base, "pr-10", className)}>{children}</code>
        <button
          type="button"
          onClick={() => doCopy(String(children))}
          title={copied ? "Скопировано" : "Копировать"}
          className="absolute right-1.5 top-1.5 rounded-md p-1.5 text-ink-muted transition hover:bg-gray-200 hover:text-accent"
        >
          {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
        </button>
      </div>
    );
  }
  return <code className={cn(base, className)}>{children}</code>;
}

/* --------------------------------------------------------- segmented control */
export function SegmentedControl({
  data,
  value,
  onChange,
  fullWidth,
}: {
  data: { value: string; label: string }[];
  value: string;
  onChange: (v: string) => void;
  fullWidth?: boolean;
}) {
  return (
    <div
      className={cn(
        "inline-flex rounded-xl bg-gray-100 p-1",
        fullWidth && "flex w-full",
      )}
    >
      {data.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded-lg px-3 py-1.5 text-sm font-semibold transition",
            fullWidth && "flex-1",
            value === o.value
              ? "bg-brand-600 text-onaccent shadow-sm"
              : "text-gray-500 hover:text-gray-700",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ avatar */
/* ------------------------------------------------------- overlay primitives */
function useLockBody(open: boolean) {
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);
}

// Shared Escape handling via a global overlay stack: each open overlay registers
// its onClose, and the single window keydown handler invokes ONLY the topmost
// (most recently opened) one. This stops one Escape press from tearing down a
// whole drawer/modal when the user only meant to close an inner popover/confirm.
const escapeStack: Array<() => void> = [];
if (typeof window !== "undefined") {
  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && escapeStack.length > 0) {
      escapeStack[escapeStack.length - 1]();
    }
  });
}
function useEscape(onClose?: () => void, active = true) {
  useEffect(() => {
    if (!active || !onClose) return;
    escapeStack.push(onClose);
    return () => {
      const i = escapeStack.lastIndexOf(onClose);
      if (i >= 0) escapeStack.splice(i, 1);
    };
  }, [onClose, active]);
}

export function Modal({
  open,
  onClose,
  title,
  children,
  dismissible = true,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title?: ReactNode;
  children: ReactNode;
  // When false the modal can't be dismissed (no X, no backdrop click, no Esc) —
  // used for blocking states like "panel restarting".
  dismissible?: boolean;
  size?: "md" | "lg" | "xl";
}) {
  useLockBody(open);
  useEscape(onClose, open && dismissible);
  if (!open) return null;
  const maxW =
    size === "xl" ? "max-w-3xl" : size === "lg" ? "max-w-2xl" : "max-w-lg";
  return createPortal(
    <div className="fixed inset-0 z-200 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 animate-fade-in bg-black/40"
        onClick={dismissible ? onClose : undefined}
      />
      <div
        className={cn(
          "relative z-10 flex max-h-[90vh] w-full animate-fade-in-up flex-col overflow-hidden rounded-2xl bg-white shadow-xl",
          maxW,
        )}
      >
        {title && (
          <div className="flex min-w-0 shrink-0 items-center justify-between gap-2 border-b border-gray-100 px-5 py-4">
            <div className="min-w-0 flex-1 text-lg font-bold text-ink">{title}</div>
            {dismissible && (
              <button
                onClick={onClose}
                className="shrink-0 text-gray-400 hover:text-gray-600"
              >
                <IconClose />
              </button>
            )}
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto p-5">{children}</div>
      </div>
    </div>,
    document.body,
  );
}

type ConfirmOpts = {
  title?: string;
  body?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
};

// useConfirm replaces window.confirm with a styled modal. Call `await confirm({…})`
// (resolves true/false) and render the returned `confirmNode` once in the tree.
export function useConfirm() {
  const [req, setReq] = useState<
    (ConfirmOpts & { resolve: (ok: boolean) => void }) | null
  >(null);
  const confirm = (opts: ConfirmOpts = {}) =>
    new Promise<boolean>((resolve) => setReq({ ...opts, resolve }));
  const close = (ok: boolean) => {
    req?.resolve(ok);
    setReq(null);
  };
  const confirmNode = (
    <Modal
      open={!!req}
      onClose={() => close(false)}
      title={req?.title ?? "Подтвердите действие"}
    >
      {req?.body && (
        <div className="text-sm leading-relaxed text-ink-muted">{req.body}</div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={() => close(false)}>
          {req?.cancelLabel ?? "Отмена"}
        </Button>
        <Button
          color={req?.danger ? "red" : "brand"}
          onClick={() => close(true)}
        >
          {req?.confirmLabel ?? "Подтвердить"}
        </Button>
      </div>
    </Modal>
  );
  return { confirm, confirmNode };
}

export function Drawer({
  open,
  onClose,
  side = "right",
  title,
  children,
  full,
}: {
  open: boolean;
  onClose: () => void;
  side?: "right" | "left";
  title?: ReactNode;
  children: ReactNode;
  full?: boolean;
}) {
  useLockBody(open);
  useEscape(onClose, open);
  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-200">
      <div
        className="absolute inset-0 animate-fade-in bg-black/40"
        onClick={onClose}
      />
      <div
        className={cn(
          "absolute top-0 flex h-full flex-col overflow-hidden bg-white shadow-xl",
          side === "right"
            ? "right-0 animate-slide-in-right"
            : "left-0 animate-slide-in-left",
          full ? "w-full" : "w-full max-w-lg",
          // Rounded inner edge on desktop only (on mobile the drawer is full-width).
          !full && (side === "right" ? "sm:rounded-l-2xl" : "sm:rounded-r-2xl"),
        )}
      >
        <div className="flex items-center justify-between border-b border-gray-100 px-4 py-3">
          <div className="min-w-0 font-bold text-ink">{title}</div>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600"
          >
            <IconClose />
          </button>
        </div>
        <div className="grow overflow-y-auto p-4">{children}</div>
      </div>
    </div>,
    document.body,
  );
}

/* ---------------------------------------------------------------- dropdown */
const DropCtx = createContext<{ close: () => void }>({ close: () => {} });

export function Dropdown({
  trigger,
  children,
  align = "end",
  width = 200,
}: {
  trigger: ReactNode;
  children: ReactNode;
  align?: "start" | "end";
  width?: number;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node))
        setOpen(false);
    };
    window.addEventListener("mousedown", h);
    return () => window.removeEventListener("mousedown", h);
  }, [open]);
  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="block"
      >
        {trigger}
      </button>
      {open && (
        <div
          style={{ width }}
          className={cn(
            "absolute z-50 mt-2 animate-scale-in overflow-hidden rounded-xl border border-gray-100 bg-white py-1 shadow-lg",
            align === "end"
              ? "right-0 origin-top-right"
              : "left-0 origin-top-left",
          )}
        >
          <DropCtx.Provider value={{ close: () => setOpen(false) }}>
            {children}
          </DropCtx.Provider>
        </div>
      )}
    </div>
  );
}

export function DropdownItem({
  children,
  onClick,
  color = "gray",
  href,
  target,
}: {
  children: ReactNode;
  onClick?: () => void;
  color?: "gray" | "red";
  href?: string;
  target?: string;
}) {
  const { close } = useContext(DropCtx);
  const cls = cn(
    "block w-full px-4 py-2 text-left text-sm transition hover:bg-gray-50",
    color === "red" ? "text-danger" : "text-ink",
  );
  const handle = () => {
    onClick?.();
    close();
  };
  if (href) {
    return (
      <a
        className={cls}
        href={href}
        target={target}
        rel="noreferrer"
        onClick={handle}
      >
        {children}
      </a>
    );
  }
  return (
    <button type="button" className={cls} onClick={handle}>
      {children}
    </button>
  );
}

export function DropdownLabel({ children }: { children: ReactNode }) {
  return (
    <div className="px-4 py-1.5 text-sm font-semibold text-gray-400">
      {children}
    </div>
  );
}

export function DropdownDivider() {
  return <hr className="my-1 border-gray-100" />;
}

/* ------------------------------------------------------------------- copy */
export function useCopy(timeout = 1500) {
  const [copied, setCopied] = useState(false);
  const copy = (value: string) => {
    navigator.clipboard?.writeText(value).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), timeout);
    }, () => {}); // ignore rejection (e.g. clipboard blocked over plain HTTP)
  };
  return { copied, copy };
}

/* -------------------------------------------------- info / document modal */
// InfoModal is the tall sectioned dialog used for read-only documents (agreement,
// donations): icon header, scrollable body, sticky footer. Omit `onClose` for a
// blocking first-run gate (no X, no backdrop dismiss); pass `footer` for actions.
export function InfoModal({
  icon,
  title,
  onClose,
  footer,
  children,
}: {
  icon?: ReactNode;
  title: ReactNode;
  onClose?: () => void;
  footer?: ReactNode;
  children: ReactNode;
}) {
  useLockBody(true); // mounted only when shown; lock scroll even for the no-onClose gate
  useEscape(onClose); // no-op when onClose is omitted (the blocking first-run gate)
  return createPortal(
    <div className="fixed inset-0 z-250 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 animate-fade-in bg-black/50"
        onClick={onClose}
      />
      <div className="relative z-10 flex max-h-[85vh] w-full max-w-2xl animate-fade-in-up flex-col overflow-hidden rounded-2xl bg-white shadow-xl">
        <div className="sticky top-0 flex items-center justify-between gap-2 border-b border-gray-100 bg-white px-5 py-4">
          <div className="flex items-center gap-2">
            {icon && <span className="text-accent">{icon}</span>}
            <h2 className="text-lg font-bold text-ink">{title}</h2>
          </div>
          {onClose && (
            <button
              onClick={onClose}
              className="text-gray-400 transition hover:text-gray-600"
            >
              <IconClose />
            </button>
          )}
        </div>
        <div className="flex flex-col gap-5 overflow-y-auto p-5">{children}</div>
        {footer && (
          <div className="sticky bottom-0 flex justify-end border-t border-gray-100 bg-white px-5 py-4">
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

// InfoSection is one titled block inside an InfoModal. `bordered` (default) renders
// the body as the brand-accented paragraph; pass false for custom content.
export function InfoSection({
  title,
  bordered = true,
  children,
}: {
  title: string;
  bordered?: boolean;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-xs font-bold uppercase tracking-wider text-accent">
        {title}
      </h3>
      {bordered ? (
        <p className="border-l-2 border-brand-100 pl-3 text-sm leading-relaxed text-ink-muted">
          {children}
        </p>
      ) : (
        children
      )}
    </section>
  );
}

/* ----------------------------------------------------------- tool dialog */
// ToolDialog is the tall full-height dialog used by the developer/ops tools (live
// logs, Xray config view): a 4xl portal with a sticky header (title + optional
// right-aligned `actions` + close, and an optional `headerExtra` second row) over
// a flex body the caller fills with its own scroll region. The body content is a
// direct child of the positioned dialog, so an absolutely-positioned overlay (e.g.
// a scroll-to-bottom button) anchors to it.
export function ToolDialog({
  title,
  actions,
  headerExtra,
  onClose,
  children,
}: {
  title: ReactNode;
  actions?: ReactNode;
  headerExtra?: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  useLockBody(true); // mounted only when shown
  useEscape(onClose);
  return createPortal(
    <div className="fixed inset-0 z-200 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 animate-fade-in bg-black/50"
        onClick={onClose}
      />
      <div className="relative z-10 flex h-[80vh] w-full max-w-4xl animate-fade-in-up flex-col overflow-hidden rounded-2xl bg-white shadow-xl">
        <div className="border-b border-gray-100 px-4 py-3 sm:px-5 sm:py-4">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-lg font-bold text-ink">{title}</h2>
            <div className="flex items-center gap-2">
              {actions}
              <button
                onClick={onClose}
                className="text-gray-400 transition hover:text-gray-600"
              >
                <IconClose />
              </button>
            </div>
          </div>
          {headerExtra && (
            <div className="no-scrollbar mt-3 overflow-x-auto">{headerExtra}</div>
          )}
        </div>
        {children}
      </div>
    </div>,
    document.body,
  );
}
