import { useCallback, useEffect, useRef, useState } from "react";

/** Demo scheduling: the in-memory check must remain visibly busy before settling. */
export const demoToolCatalogRefreshDelayMs = 600;

export type ToolCatalogDemoProvider = {
  /** ISO timestamp of the last successful in-memory check, or empty before one. */
  checkedAt: string;
  /** True while a demo refresh is pending; the shared summary disables its button. */
  busy: boolean;
  /** Starts one in-memory check; ignores presses while one is pending. */
  refresh: () => void;
};

/**
 * In-memory catalog-check lifecycle for the prototype Settings host. It mirrors
 * the real manual refresh: the shared summary shows a pending state, then the
 * last successful check time. It never changes the canonical demo release
 * catalog and never installs or activates a package.
 */
export function useToolCatalogDemoProvider({
  delayMs = demoToolCatalogRefreshDelayMs,
}: {
  /** Test and live-check override; the default keeps the busy state visible. */
  delayMs?: number;
} = {}): ToolCatalogDemoProvider {
  const [checkedAt, setCheckedAt] = useState("");
  const [busy, setBusy] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(
    () => () => {
      if (timer.current !== undefined) clearTimeout(timer.current);
    },
    [],
  );

  const refresh = useCallback(() => {
    if (timer.current !== undefined) return;
    setBusy(true);
    timer.current = setTimeout(() => {
      timer.current = undefined;
      setCheckedAt(new Date().toISOString());
      setBusy(false);
    }, delayMs);
  }, [delayMs]);

  return { checkedAt, busy, refresh };
}
