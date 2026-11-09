import { Component, type ErrorInfo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "./ui";

// ErrorBoundary is the difference between a broken screen and a blank one. Without it
// any exception thrown while rendering — a component reading a field the server stopped
// sending, a lazy chunk whose file an upgrade replaced — unmounts the whole tree and
// leaves the operator staring at a white page with nothing to report.
//
// It is mounted twice: once around the whole app (main.tsx) and once around the active
// section in Dashboard, where it is keyed on the tab. That second one is the useful
// placement — a section that fails leaves the sidebar, the header and the tab bar
// usable, and switching to another section remounts the boundary with a clean state,
// so the operator recovers without reloading.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The panel runs on the operator's own box and has no error collector; the
    // console and the details block below are the report. React already logs the
    // error itself — this adds the component stack, which is what makes it fixable.
    console.error("panel: render failed", error, info.componentStack);
  }

  render() {
    if (this.state.error) return <Crashed error={this.state.error} />;
    return this.props.children;
  }
}

function Crashed({ error }: { error: Error }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-2 px-4 py-16 text-center">
      <p className="text-sm font-semibold text-ink">{t("common.crashTitle")}</p>
      <p className="max-w-md text-xs text-ink-muted">{t("common.crashHint")}</p>
      <div className="mt-2">
        <Button onClick={() => window.location.reload()}>{t("common.crashReload")}</Button>
      </div>
      {/* Collapsed by default: an operator needs the way out, but whoever is asked to
          fix it needs the message, and asking them to open the console loses it. */}
      <details className="mt-4 w-full max-w-xl text-start">
        <summary className="cursor-pointer select-none text-[11px] text-ink-muted">
          {t("common.crashDetails")}
        </summary>
        <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-100 p-2 text-[11px] text-ink-muted">
          {error.message}
        </pre>
      </details>
    </div>
  );
}
