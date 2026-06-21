import { useEffect, useRef, useState } from "react";
import { cn, SegmentedControl, ToolDialog } from "./ui";

// LogViewer is the live-tailing log dialog shared by the panel and Xray log views.
// Each caller supplies the SSE url, title, filter tabs, a `classify` fn mapping a
// line to a category, and `colorOf` mapping a category to a text-color class.
export function LogViewer({
  title,
  streamUrl,
  onClose,
  filters,
  classify,
  colorOf,
}: {
  title: string;
  streamUrl: string;
  onClose: () => void;
  filters: { value: string; label: string }[];
  classify: (line: string) => string;
  colorOf: (cat: string) => string;
}) {
  const [lines, setLines] = useState<string[]>([]);
  const [level, setLevel] = useState("all");
  const [atBottom, setAtBottom] = useState(true);
  const boxRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    const es = new EventSource(streamUrl, { withCredentials: true });
    es.onmessage = (e) => {
      setLines((prev) => {
        const next = [...prev, e.data];
        return next.length > 2000 ? next.slice(-2000) : next;
      });
    };
    return () => es.close();
  }, [streamUrl]);

  const shown =
    level === "all" ? lines : lines.filter((l) => classify(l) === level);

  // Auto-scroll to the bottom unless the user scrolled up to read history.
  useEffect(() => {
    if (stick.current && boxRef.current) {
      boxRef.current.scrollTop = boxRef.current.scrollHeight;
    }
  }, [shown.length]);

  const onScroll = () => {
    const el = boxRef.current;
    if (!el) return;
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
    stick.current = bottom;
    setAtBottom(bottom);
  };

  const scrollToBottom = () => {
    const el = boxRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    stick.current = true;
    setAtBottom(true);
  };

  return (
    <ToolDialog
      title={title}
      onClose={onClose}
      headerExtra={
        <SegmentedControl data={filters} value={level} onChange={setLevel} />
      }
    >
      <div
        ref={boxRef}
        onScroll={onScroll}
        className="flex-1 overflow-auto bg-gray-50 p-3 font-mono text-xs leading-relaxed"
      >
        {shown.length === 0 ? (
          <p className="text-gray-400">
            {lines.length === 0
              ? "Ожидание логов…"
              : "Нет строк выбранного уровня"}
          </p>
        ) : (
          shown.map((l, i) => (
            <div
              key={i}
              className={cn("whitespace-pre-wrap break-all", colorOf(classify(l)))}
            >
              {l}
            </div>
          ))
        )}
      </div>
      {!atBottom && (
        <button
          onClick={scrollToBottom}
          aria-label="Прокрутить вниз"
          className="absolute bottom-4 right-4 z-20 flex h-10 w-10 items-center justify-center rounded-full bg-brand-600 text-white shadow-lg transition hover:bg-brand-700"
        >
          <svg
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M12 5v14M5 12l7 7 7-7" />
          </svg>
        </button>
      )}
    </ToolDialog>
  );
}
