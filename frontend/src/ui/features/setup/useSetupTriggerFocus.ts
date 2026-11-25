import { useCallback, useRef } from "react";

/**
 * Remembers the control that started a managed-tool install and returns focus
 * to it after the confirmation closes.
 */
export function useSetupTriggerFocus() {
  const trigger = useRef<HTMLElement | null>(null);
  const capture = useCallback((target: EventTarget | null) => {
    trigger.current = target instanceof HTMLElement ? target : null;
  }, []);
  const restore = useCallback(() => {
    const element = trigger.current;
    requestAnimationFrame(() => {
      if (element?.isConnected) element.focus();
    });
  }, []);
  return { capture, restore };
}
