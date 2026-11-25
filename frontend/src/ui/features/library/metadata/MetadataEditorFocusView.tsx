import type { ReactNode } from "react";
import { useEffect, useRef } from "react";

/**
 * Focus target for an independently mounted metadata editor. The host only
 * says whether an incoming link should focus the editor; the wrapper keeps the
 * same layout as the release editor and moves focus and scroll on arrival.
 */
export function MetadataEditorFocusView({
  focus = false,
  children,
}: {
  focus?: boolean;
  children: ReactNode;
}) {
  const target = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!focus) return;
    target.current?.focus({ preventScroll: true });
    target.current?.scrollIntoView?.({ block: "start" });
  }, [focus]);
  return (
    <div
      ref={target}
      tabIndex={focus ? -1 : undefined}
      className="grid min-w-0 gap-3"
    >
      {children}
    </div>
  );
}
