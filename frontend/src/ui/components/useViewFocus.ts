import { type RefObject, useEffect, useLayoutEffect } from "react";

export function focusView(
  target: HTMLElement | null | undefined,
  {
    scroll,
    preventScroll,
    scrollFirst,
  }: {
    scroll?: ScrollIntoViewOptions;
    preventScroll?: boolean;
    scrollFirst?: boolean;
  } = {},
) {
  if (!target) return false;
  if (scroll && scrollFirst) target.scrollIntoView?.(scroll);
  if (preventScroll === undefined) target.focus();
  else target.focus({ preventScroll });
  if (scroll && !scrollFirst) target.scrollIntoView?.(scroll);
  return true;
}

export function useViewFocus(
  ref: RefObject<HTMLElement | null>,
  key: string | boolean | null | undefined,
  scrollToStart = false,
) {
  useEffect(() => {
    if (!key) return;
    focusView(ref.current, {
      scroll: scrollToStart ? { block: "start" } : undefined,
      preventScroll: scrollToStart ? true : undefined,
    });
  }, [key, ref, scrollToStart]);
}

export function focusViewById(id: string) {
  return focusView(document.getElementById(id));
}

export function usePreviewHeadingFocus(
  ref: RefObject<HTMLElement | null>,
  sequence: number,
) {
  useLayoutEffect(() => {
    if (!sequence) return;
    const reduced = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    focusView(ref.current, {
      scroll: { behavior: reduced ? "auto" : "smooth", block: "start" },
    });
  }, [ref, sequence]);
}
