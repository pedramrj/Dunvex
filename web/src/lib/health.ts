import { useCallback, useEffect, useRef, useState } from "react";
import { api, type HealthReport, type HealthResult } from "./api";

/**
 * The config health check: the last report, refreshed every minute and every
 * two seconds while a check runs. `check()` starts one now.
 */
export function useHealth(enabled = true) {
  const [report, setReport] = useState<HealthReport | null>(null);
  const [err, setErr] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const alive = useRef(true);

  const load = useCallback(() => {
    clearTimeout(timer.current);
    return api<HealthReport>("health")
      .then((r) => {
        if (!alive.current) return;
        setReport(r);
        setErr("");
        timer.current = setTimeout(load, r.running ? 2000 : 60000);
      })
      .catch((e) => {
        if (!alive.current) return;
        setErr(e.message);
        timer.current = setTimeout(load, 60000);
      });
  }, []);

  useEffect(() => {
    alive.current = true;
    if (enabled) load();
    return () => { alive.current = false; clearTimeout(timer.current); };
  }, [enabled, load]);

  const check = useCallback(
    () => api<HealthReport>("health/check", { method: "POST" }).then((r) => { setReport(r); load(); }),
    [load],
  );
  return { report, err, check, reload: load };
}

const rank = { fail: 2, ok: 1, skip: 0 } as const;

/** The worst result of each inbound (a config can have CDN and relay copies). */
export function worstByInbound(results: HealthResult[]): Record<number, HealthResult & { copies: number; failing: number }> {
  const out: Record<number, HealthResult & { copies: number; failing: number }> = {};
  for (const r of results) {
    const cur = out[r.inboundId];
    if (!cur) out[r.inboundId] = { ...r, copies: 1, failing: r.status === "fail" ? 1 : 0 };
    else {
      const next = rank[r.status] > rank[cur.status] ? { ...r, copies: cur.copies, failing: cur.failing } : cur;
      next.copies = cur.copies + 1;
      next.failing = cur.failing + (r.status === "fail" ? 1 : 0);
      out[r.inboundId] = next;
    }
  }
  return out;
}

/** Results to show first: failures, then working ones by speed, then the untested. */
export function sortResults(results: HealthResult[]): HealthResult[] {
  return [...results].sort((a, b) => rank[b.status] - rank[a.status] || (a.status === "ok" ? (a.ms ?? 0) - (b.ms ?? 0) : a.name.localeCompare(b.name)));
}

/** Latency colour: quick or slow. Red is kept for configs that fail. */
export const msColor = (ms = 0) => (ms < 1000 ? "var(--ok)" : "var(--warn)");
