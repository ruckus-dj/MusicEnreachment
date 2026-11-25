import { useEffect } from "react";

function currentHeading() {
  return (
    document.querySelector<HTMLElement>("#content h1") ??
    document.querySelector<HTMLElement>("h1")
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
  }, [focusKey]);
  useEffect(() => {
    if (!titleKey) return;
    const heading = currentHeading();
    if (heading) document.title = `${heading.textContent} · ${titleSuffix}`;
  }, [titleKey, titleSuffix]);
}
