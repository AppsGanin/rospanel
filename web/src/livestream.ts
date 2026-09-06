// A Server-Sent Events subscription that survives being refused.
//
// EventSource reconnects on its own when the TRANSPORT drops, which is what the
// browser does for a closed socket or a timeout. It does NOT reconnect when the
// server answers with an HTTP error: a non-2xx makes it fire `error` and set
// readyState to CLOSED, permanently. The panel refuses a stream with 429 once the
// per-IP gate is full (eight, counted across every stream a browser holds — the
// dashboard and the log viewer each take one, so four tabs is the limit), and the
// symptom of hitting that is the worst kind: the dashboard freezes on its last frame
// showing stale user counts and stale traffic, with nothing on screen to say so.
//
// So the retry is ours. Backoff rather than a tight loop, because the refusal is a
// capacity signal and hammering it keeps the gate full; capped, because the operator
// closing a tab is what frees a slot and they may take a while.
const retryStart = 2000;
const retryMax = 30000;

export interface LiveStream {
  close: () => void;
}

// openStream subscribes to url and keeps the subscription alive. onMessage receives
// each frame's data; onState is told when the connection comes up or goes down, so a
// panel can say "reconnecting" instead of quietly showing an old number.
export function openStream(
  url: string,
  onMessage: (data: string) => void,
  onState?: (live: boolean) => void,
): LiveStream {
  let es: EventSource | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let delay = retryStart;
  let closed = false;

  const connect = () => {
    if (closed) return;
    es = new EventSource(url, { withCredentials: true });
    es.onopen = () => {
      delay = retryStart; // a good connection earns the short retry back
      onState?.(true);
    };
    es.onmessage = (e) => onMessage(e.data);
    es.onerror = () => {
      // Fires for a refused stream and for a dropped one alike, and in both cases the
      // browser has already given up on this EventSource. Close it explicitly so a
      // half-open one cannot linger, then retry on our own clock.
      es?.close();
      es = null;
      onState?.(false);
      if (closed) return;
      timer = setTimeout(connect, delay);
      delay = Math.min(delay * 2, retryMax);
    };
  };

  connect();
  return {
    close: () => {
      closed = true;
      if (timer !== undefined) clearTimeout(timer);
      es?.close();
    },
  };
}
