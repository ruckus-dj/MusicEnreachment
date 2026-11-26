import { type ReactNode, useId } from "react";
import { localReleaseMessages } from "../local-release/localReleaseMessages";
import {
  ManualReleasePreparationOutlet,
  type ManualReleasePreparationSource,
} from "./ManualReleasePreparationOutlet";
import {
  ManualSearchOutlet,
  type ManualSearchSource,
} from "./ManualSearchOutlet";
import { matchingCandidatesPanel } from "./MatchingWorkspaceLayout";

export type MatchingReviewSearchViewProps = {
  locale: "en" | "ru";
  search: ManualSearchSource;
  preparation: ManualReleasePreparationSource;
  createLocalReleaseHref?: string;
  children?: ReactNode;
};

/**
 * Shared shell for the manual release search and preparation pair. Queries,
 * drafts, pending operations and selection authority stay with the bound
 * controllers; this module owns the heading, both portable mounts and the
 * two-column layout.
 */
export function MatchingReviewSearchView({
  locale,
  search,
  preparation,
  createLocalReleaseHref,
  children,
}: MatchingReviewSearchViewProps) {
  const headingId = useId();
  return (
    <section
      data-matching-candidates
      className={matchingCandidatesPanel}
      aria-labelledby={headingId}
    >
      <h2
        id={headingId}
        data-matching-search-heading
        className="m-0 mb-4 text-base font-semibold"
      >
        {locale === "ru" ? "Найти издание" : "Find a release"}
      </h2>
      <div className="grid min-w-0 items-start gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <ManualSearchOutlet source={search} locale={locale} />
        <ManualReleasePreparationOutlet source={preparation} locale={locale} />
      </div>
      {createLocalReleaseHref && (
        <div className="mt-4 flex">
          <a
            className="inline-flex min-h-9 w-fit max-w-full items-center justify-center rounded-lg border border-[var(--line)] px-3 py-1.5 text-center text-sm font-medium text-[var(--ink)] no-underline hover:border-[var(--control)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] max-[520px]:w-full"
            href={createLocalReleaseHref}
          >
            {localReleaseMessages[locale].title}
          </a>
        </div>
      )}
      {children}
    </section>
  );
}
