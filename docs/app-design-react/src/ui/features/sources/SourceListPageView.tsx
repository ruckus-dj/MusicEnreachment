import type { ComponentProps } from "react";
import {
  SourceCleanupOutlet,
  type SourceCleanupSource,
} from "./SourceCleanupOutlet";
import { SourceListHeadingView } from "./SourceListHeadingView";
import {
  SourceNoticeView,
  SourcePageFeedbackView,
} from "./SourcePageFeedbackView";
import {
  SourceRegistrationView,
  sourceRegistrationMessages,
} from "./SourceRegistrationView";
import { sourcePublicationsNote } from "./SourceRootDetailView";
import { SourceRootList } from "./SourceRootList";

/** Structured provider feedback, or a demo-only persistent status notice. */
export type SourceListPageFeedback =
  | ComponentProps<typeof SourcePageFeedbackView>
  | string;

export type SourceListPageViewProps = {
  locale: "en" | "ru";
  ariaBusy?: boolean;
  heading: Omit<ComponentProps<typeof SourceListHeadingView>, "locale">;
  registration?: Omit<ComponentProps<typeof SourceRegistrationView>, "locale">;
  feedback?: SourceListPageFeedback;
  rows: ComponentProps<typeof SourceRootList>["rows"];
  listState?: ComponentProps<typeof SourceRootList>["state"];
  onScan: (id: string) => void;
  listDisabled?: boolean;
  loaded: boolean;
  cleanup?: SourceCleanupSource;
};

// One list-page order for the prototype and the application: heading,
// registration, feedback, the loaded table, and guarded cleanup.
// Adapters supply typed models and handlers; the cleanup
// binder keeps its hook mounted only while the loaded list owns the page.
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
}: SourceListPageViewProps) {
  return (
    <section
      aria-labelledby="sources-title"
      aria-busy={ariaBusy || undefined}
      className="mx-auto grid w-full min-w-0 max-w-5xl gap-[14px] py-3"
    >
      <SourceListHeadingView {...heading} locale={locale} />
      {registration && (
        <SourceRegistrationView {...registration} locale={locale} />
      )}
      {typeof feedback === "string" ? (
        <SourceNoticeView message={feedback} />
      ) : (
        feedback && <SourcePageFeedbackView {...feedback} />
      )}
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
      {loaded && cleanup && <SourceCleanupOutlet source={cleanup} />}
      {loaded && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
    </section>
  );
}
