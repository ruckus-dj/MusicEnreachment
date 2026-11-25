import type { ComponentProps, ReactNode } from "react";
import { sourceRegistrationMessages } from "./SourceRegistrationView";
import { sourcePublicationsNote } from "./SourceRootDetailView";
import { SourceRootList } from "./SourceRootList";

export type SourceListPageViewProps = {
  locale: "en" | "ru";
  ariaBusy?: boolean;
  heading: ReactNode;
  registration?: ReactNode;
  feedback?: ReactNode;
  rows: ComponentProps<typeof SourceRootList>["rows"];
  listState?: ComponentProps<typeof SourceRootList>["state"];
  onScan: (id: string) => void;
  listDisabled?: boolean;
  loaded: boolean;
  cleanup?: ReactNode;
  footer?: ReactNode;
};

// One list-page order for the prototype and the application: heading,
// registration, provider feedback, the loaded table, cleanup, and footer. The
// adapter owns each slot's content and handlers.
export function SourceListPageView({
  locale,
  ariaBusy,
  heading,
  registration,
  feedback,
  rows,
  listState,
  onScan,
  listDisabled,
  loaded,
  cleanup,
  footer,
}: SourceListPageViewProps) {
  return (
    <section
      aria-labelledby="sources-title"
      aria-busy={ariaBusy || undefined}
      className="mx-auto grid w-full min-w-0 max-w-5xl gap-[14px] py-3"
    >
      {heading}
      {registration}
      {feedback}
      {loaded && (
        <p className="my-2 text-xs text-[var(--muted)]">
          {sourceRegistrationMessages[locale].sourceTableHelp}
        </p>
      )}
      <fieldset disabled={listDisabled} className="m-0 min-w-0 border-0 p-0">
        <SourceRootList
          rows={rows}
          locale={locale}
          state={listState}
          onScan={onScan}
        />
      </fieldset>
      {cleanup}
      {loaded && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
      {footer}
    </section>
  );
}
