import { useEffect } from "react";

type AlertRef = { readonly current: HTMLElement | null };

/**
 * Moves focus to a setup alert after a new failure is reported. Controllers
 * keep the feedback state; this hook keeps the established focus timing in the
 * portable UI tree.
 */
export function useSetupAlertFocus(alert: AlertRef, failure: unknown) {
  useEffect(() => {
    if (failure) alert.current?.focus();
  }, [alert, failure]);
}
