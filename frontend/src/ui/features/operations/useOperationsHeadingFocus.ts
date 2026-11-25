import { useRef } from "react";

/**
 * Keeps operations heading focus restoration in the portable UI tree, matching
 * the list's previous action timing and its focus-ownership check.
 */
export function useOperationsHeadingFocus() {
  const headingRef = useRef<HTMLHeadingElement>(null);

  function focusHeading() {
    headingRef.current?.focus({ preventScroll: true });
  }

  function restoreHeadingFocus(trigger: Element) {
    const owner = trigger.closest("button") ?? trigger;
    const active = document.activeElement;
    if (owner.isConnected && active !== owner && !owner.contains(active))
      return;
    focusHeading();
  }

  return { headingRef, focusHeading, restoreHeadingFocus };
}
