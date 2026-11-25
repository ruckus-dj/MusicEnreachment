import type { ReactNode } from "react";
import type { Locale } from "../../i18n/types";
import { TrackScreenView, type TrackScreenViewProps } from "./TrackScreenView";

export function LibraryReleaseWorkspaceView({
  locale,
  overview,
  showPosition,
  missingPosition,
  children,
}: {
  locale: Locale;
  overview: ReactNode;
  showPosition: boolean;
  missingPosition?: TrackScreenViewProps["notFound"];
  children: ReactNode;
}) {
  return (
    <section
      className={`grid min-w-0 gap-3 text-[13px] leading-[18px] text-ink ${showPosition ? "" : "mx-auto max-w-5xl"}`}
    >
      <div hidden={showPosition}>{overview}</div>
      {missingPosition && (
        <TrackScreenView locale={locale} notFound={missingPosition} />
      )}
      {children}
    </section>
  );
}
