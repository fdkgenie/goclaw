import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet } from "./client";
import type { Delta, Snapshot } from "./types";

export interface UseSSEResult<T extends { version: number }> {
  snapshot: T | null;
  version: number | null;
  stale: boolean;
}

export function useSSE<T extends { version: number }>(
  path: string,
  initial: T,
): UseSSEResult<T> {
  const [snapshot, setSnapshot] = useState<T | null>(null);
  const [version, setVersion] = useState<number | null>(null);
  const [stale, setStale] = useState(false);
  const versionRef = useRef<number | null>(null);

  useEffect(() => {
    let es: EventSource | null = null;
    let cancelled = false;

    const init = async () => {
      try {
        const snap = (await apiGet<T>(`${path}?snapshot=1`)) as Snapshot & T;
        if (!cancelled) {
          setSnapshot(snap);
          setVersion(snap.version);
          versionRef.current = snap.version;
          setStale(false);
        }
      } catch {
        // snapshot fetch failed; stream will still deliver deltas
      }
    };
    init();

    es = new EventSource(`${path}?stream=1`);
    es.onmessage = (e) => {
      try {
        const delta = JSON.parse(e.data) as Delta & { data: T };
        if (delta.version != null && versionRef.current != null) {
          if (delta.version <= versionRef.current) {
            return; // stale/duplicate delta
          }
        }
        if (!cancelled && delta.data) {
          setSnapshot((prev) => ({ ...prev, ...delta.data }));
          setVersion(delta.version);
          versionRef.current = delta.version;
          setStale(false);
        }
      } catch {
        // ignore malformed delta
      }
    };
    es.onerror = () => {
      if (!cancelled) setStale(true);
      es?.close();
    };

    return () => {
      cancelled = true;
      es?.close();
    };
  }, [path]);

  return { snapshot, version, stale };
}
