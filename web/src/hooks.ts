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
