import { useEffect } from "react";

function visibleHeading(root: ParentNode) {
  for (const heading of root.querySelectorAll<HTMLElement>("h1")) {
    if (!heading.closest("[hidden]")) return heading;
  }
  return undefined;
}

function currentHeading() {
  const content = document.getElementById("content");
  return (
    (content ? visibleHeading(content) : undefined) ?? visibleHeading(document)
  );
}

export function useShellPageEffects({
  focusKey,
  titleKey,
  titleSuffix,
  scrollResetKey,
}: {
  focusKey: string;
  titleKey: string;
  titleSuffix: string;
  scrollResetKey?: string;
}) {
  useEffect(() => {
    if (!scrollResetKey) return;
    const content = document.getElementById("content");
    if (content) content.scrollTop = 0;
    if (document.scrollingElement) document.scrollingElement.scrollTop = 0;
  }, [scrollResetKey]);
  useEffect(() => {
    if (!focusKey) return;
    const heading = currentHeading();
    if (!heading) return;
    heading.tabIndex = -1;
    heading.focus({ preventScroll: true });
    // Closing a portaled control during navigation can leave focus on body.
    // Retry after its cleanup, without moving focus from a visible control.
    const focusRecovery = window.requestAnimationFrame(() => {
      const active = document.activeElement;
      if (
        currentHeading() === heading &&
        (active === document.body || active?.closest("[hidden]"))
      ) {
        heading.focus({ preventScroll: true });
      }
    });
    return () => window.cancelAnimationFrame(focusRecovery);
  }, [focusKey]);
  useEffect(() => {
    if (!titleKey) return;
    const heading = currentHeading();
    if (heading) document.title = `${heading.textContent} · ${titleSuffix}`;
  }, [titleKey, titleSuffix]);
}
