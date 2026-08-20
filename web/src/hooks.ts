// Shared data-fetching and async-action hooks. Every panel previously hand-rolled
// the same loading/busy/error-toast boilerplate; these collapse it to one line.

import { useEffect, useState } from "react";
import { errMessage, notifyError } from "./notify";

// useFetch runs `fn` on mount (and when `deps` change), exposing the result, a
// `loaded` flag for the initial <CenterLoader/> gate, and a setter for optimistic
// updates. Errors are swallowed (the panel renders its empty state); use useAction
// for user-triggered calls that should surface a toast.
export function useFetch<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let alive = true;
    fn()
      .then((d) => alive && setData(d))
      .catch(() => {})
      .finally(() => alive && setLoaded(true));
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return { data, loaded, setData };
}

// useDirtyForm<T> tracks a draft value and its last-committed snapshot.
// `load(v)` sets both when the server response arrives; `commit()` advances the
// snapshot after a successful save; `reset()` discards edits on cancel.
// `isDirty` is true while draft differs from the snapshot (JSON comparison).
export function useDirtyForm<T>(initial: T) {
  const [draft, setDraft] = useState<T>(initial);
  const [saved, setSaved] = useState<T>(initial);
  return {
    draft,
    setDraft,
    saved,
    isDirty: JSON.stringify(draft) !== JSON.stringify(saved),
    load: (v: T) => { setDraft(v); setSaved(v); },
    commit: () => setSaved(draft),
    reset: () => setDraft(saved),
  };
}

// useAction wraps a user-triggered async call with busy state and an error toast.
// In-flight actions are tracked as a Set of keys (not a single slot), so when a
// panel fires several keyed actions at once each keeps its own spinner — the first
// to finish no longer clears the others. `busy` is "anything running"; `isBusy(key)`
// is per-button.
export function useAction() {
  const [keys, setKeys] = useState<Set<string>>(() => new Set());
  const run = async (
    fn: () => Promise<void>,
    opts: { key?: string; errMsg?: string } = {},
  ) => {
    const key = opts.key ?? "";
    setKeys((s) => new Set(s).add(key));
    try {
      await fn();
    } catch (e) {
      notifyError(errMessage(e, opts.errMsg));
    } finally {
      setKeys((s) => {
        const n = new Set(s);
        n.delete(key);
        return n;
      });
    }
  };
  return { busy: keys.size > 0, isBusy: (key: string) => keys.has(key), run };
}

// useShowMore renders a long list in chunks: `first` rows up front, `step` more per
// click on the <ShowMore/> button that pairs with it. Client-side on purpose — the
// lists that use it are already fully loaded (the server caps each response), so
// what this buys is a card the operator can read past, not fewer requests.
//
// `resetKey` collapses back to `first` when the list starts being about something
// else — another user's card, another search — instead of carrying one expansion
// into the next. Without it a reopened modal would show the previous card's depth.
export function useShowMore<T>(
  items: T[],
  {
    first = 20,
    step = 20,
    resetKey,
  }: { first?: number; step?: number; resetKey?: unknown } = {},
) {
  const [limit, setLimit] = useState(first);
  useEffect(() => {
    setLimit(first);
  }, [resetKey, first]);
  return {
    shown: items.length > limit ? items.slice(0, limit) : items,
    rest: Math.max(0, items.length - limit),
    showMore: () => setLimit((n) => n + step),
  };
}

// ViewMode is how a list is drawn. The table is the default everywhere: it is the denser
// form, and these are lists whose rows all carry the same facts.
export type ViewMode = "table" | "cards";

// useViewMode remembers the operator's choice per list, in this browser.
//
// It is a working preference, not a per-visit one — an operator who prefers cards should
// not have to say so on every page load. Storage can throw (Safari in private mode denies
// access outright), and a preference is never worth failing a render over, so every touch
// is guarded and an unreadable store simply means "the default".
export function useViewMode(key: string): [ViewMode, (v: string) => void] {
  const storageKey = `rospanel.view.${key}`;
  const [view, setView] = useState<ViewMode>(() => {
    try {
      return localStorage.getItem(storageKey) === "cards" ? "cards" : "table";
    } catch {
      return "table";
    }
  });
  const change = (v: string) => {
    const next: ViewMode = v === "cards" ? "cards" : "table";
    setView(next);
    try {
      localStorage.setItem(storageKey, next);
    } catch {
      // Not remembered; the session still works.
    }
  };
  return [view, change];
}
