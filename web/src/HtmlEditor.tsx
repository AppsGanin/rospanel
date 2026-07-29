import { useRef } from "react";
import i18n from "./i18n";
import { Textarea } from "./ui";

// Icons in the stroke-and-currentColor style the rest of the panel uses. The
// letter-shaped controls deliberately render in the style they apply — B bold, I
// italic — which says what they do without needing a legend.
const IconCode = () => (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <path d="m9 8-4 4 4 4M15 8l4 4-4 4" />
  </svg>
);

const IconLink = () => (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.5 1.5" />
    <path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7L12 19" />
  </svg>
);

const IconSpoiler = () => (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <path d="M9.9 4.2A10 10 0 0 1 12 4c6 0 10 8 10 8a17 17 0 0 1-2.6 3.6M6.6 6.6A17 17 0 0 0 2 12s4 8 10 8a10 10 0 0 0 4.5-1.1" />
    <path d="M10.6 10.6a2 2 0 0 0 2.8 2.8M2 2l20 20" />
  </svg>
);

// The formatting Telegram actually accepts. Anything outside this list arrives as
// literal text, so offering more would only produce broken-looking messages.
//
// Deliberately a tag-wrapper and not a Markdown editor: Telegram parses a small fixed
// subset of HTML, and a Markdown converter would add a layer whose mistakes surface
// only once the message is already in someone's chat.
const formats = (): {
  key: string;
  title: string;
  open: string;
  close: string;
  placeholder: string;
  content: React.ReactNode;
}[] => [
  {
    key: "b", title: i18n.t("html.bold"), open: "<b>", close: "</b>", placeholder: i18n.t("html.text"),
    content: <span className="font-bold">{i18n.t("html.boldGlyph")}</span>,
  },
  {
    key: "i", title: i18n.t("html.italic"), open: "<i>", close: "</i>", placeholder: i18n.t("html.text"),
    content: <span className="italic">{i18n.t("html.italicGlyph")}</span>,
  },
  {
    key: "u", title: i18n.t("html.underline"), open: "<u>", close: "</u>", placeholder: i18n.t("html.text"),
    content: <span className="underline">{i18n.t("html.underlineGlyph")}</span>,
  },
  {
    key: "s", title: i18n.t("html.strike"), open: "<s>", close: "</s>", placeholder: i18n.t("html.text"),
    content: <span className="line-through">S</span>,
  },
  {
    key: "code", title: i18n.t("html.mono"), open: "<code>", close: "</code>", placeholder: i18n.t("html.code"),
    content: <IconCode />,
  },
  {
    key: "a", title: i18n.t("html.link"), open: '<a href="https://">', close: "</a>",
    placeholder: i18n.t("html.linkText"), content: <IconLink />,
  },
  {
    key: "spoiler", title: i18n.t("html.spoiler"), open: "<tg-spoiler>", close: "</tg-spoiler>",
    placeholder: i18n.t("html.spoilerText"), content: <IconSpoiler />,
  },
];

// HtmlEditor is a textarea with a formatting bar, shared by the broadcast composer
// and the single-user message so both offer the same tags and behave identically.
export function HtmlEditor({
  label,
  value,
  onChange,
  rows = 5,
  placeholder,
}: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  rows?: number;
  placeholder?: string;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);

  // wrap puts the selection inside a tag pair, or inserts a placeholder when nothing
  // is selected, leaving it selected so typing replaces it.
  const wrap = (open: string, close: string, placeholder: string) => {
    const el = ref.current;
    if (!el) return;
    const from = el.selectionStart;
    const to = el.selectionEnd;
    const chosen = value.slice(from, to) || placeholder;
    onChange(value.slice(0, from) + open + chosen + close + value.slice(to));
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(from + open.length, from + open.length + chosen.length);
    });
  };

  return (
    <div>
      {label && <p className="mb-1 text-sm font-medium text-ink">{label}</p>}
      <div className="mb-1 flex flex-wrap gap-1">
        {formats().map((f) => (
          <button
            key={f.key}
            type="button"
            title={f.title}
            aria-label={f.title}
            onClick={() => wrap(f.open, f.close, f.placeholder)}
            className="flex h-8 w-8 items-center justify-center rounded-md border border-gray-200 text-sm text-ink transition hover:border-accent hover:text-accent"
          >
            {f.content}
          </button>
        ))}
      </div>
      <Textarea
        value={value}
        onChange={onChange}
        rows={rows}
        inputRef={ref}
        placeholder={placeholder}
      />
    </div>
  );
}
