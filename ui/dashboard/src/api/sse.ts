import { useEffect, useRef, useState } from "react";

export interface UseSSEResult<T> {
  snapshot: T | null;
  version: number | null;
  stale: boolean;
}

interface StreamEnvelope<T> {
  event: "snapshot" | "delta" | "keepalive" | "down";
  data: {
    version?: number;
    kind?: string;
    kpi?: T;
  } | null;
}

export function useSSE<T>(endpoint = "/api/stream"): UseSSEResult<T> {
  const [snapshot, setSnapshot] = useState<T | null>(null);
  const [version, setVersion] = useState<number | null>(null);
  const [stale, setStale] = useState(false);
  const versionRef = useRef<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    const es = new EventSource(endpoint);

    es.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data) as StreamEnvelope<T>;
        if (msg.event === "snapshot") {
          if (cancelled || !msg.data) return;
          const nextVersion = msg.data.version ?? 0;
          versionRef.current = nextVersion;
          setVersion(nextVersion);
          if (msg.data.kpi) setSnapshot(msg.data.kpi);
          setStale(false);
          return;
        }
        if (msg.event === "delta") {
          if (cancelled || !msg.data) return;
          const nextVersion = msg.data.version ?? 0;
          if (versionRef.current != null && nextVersion <= versionRef.current) {
            return; // stale/duplicate delta
          }
          versionRef.current = nextVersion;
          setVersion(nextVersion);
          if (msg.data.kpi) setSnapshot(msg.data.kpi);
          // A "down" delta means the gateway connection dropped and the
          // tenant canary has not re-verified: the stream is degraded.
          setStale(msg.data.kind === "down");
        }
      } catch {
        // ignore malformed frames
      }
    };

    es.onerror = () => {
      if (!cancelled) setStale(true);
    };

    return () => {
      cancelled = true;
      es.close();
    };
  }, [endpoint]);

  return { snapshot, version, stale };
}
