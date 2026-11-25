import type { ComponentProps } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import { catalogueMessage } from "./catalogueMessages";
import {
  type LibraryArtistReleaseTrackRow,
  LibraryArtistReleaseTracksView,
} from "./LibraryArtistReleaseTracksView";
import { LibraryPaginationControls } from "./LibraryPaginationView";

/** Separate positions transport states; the edition heading stays outside. */
export type ArtistReleasePositionsModel =
  | { state: "loading" }
  | {
      state: "error";
      message: string;
      onRetry: () => void;
    }
  | {
      state: "ready";
      tracks: readonly LibraryArtistReleaseTrackRow[];
      pagination?: ComponentProps<typeof LibraryPaginationControls> | null;
    };

const errorPanelClass =
  "grid justify-items-start gap-2 rounded-md border border-[var(--error)] bg-[var(--surface)] p-4 text-sm text-[var(--error)] [&>p]:m-0";

/**
 * Edition positions surface. The host owns loading, retry and page state; this
 * module renders the shared track table, pagination and status copy for one
 * expanded edition.
 */
export function ArtistReleasePositionsView({
  locale,
  releaseTitle,
  positions,
  releaseSelection,
}: {
  locale: Locale;
  releaseTitle: string;
  positions: ArtistReleasePositionsModel;
  releaseSelection?: ComponentProps<
    typeof LibraryArtistReleaseTracksView
  >["releaseSelection"];
}) {
  if (positions.state === "loading")
    return <p role="status">{catalogueMessage(locale, "loadingPositions")}</p>;
  if (positions.state === "error")
    return (
      <section className={errorPanelClass} role="alert">
        <p>{positions.message}</p>
        <AppButton onPress={positions.onRetry}>
          {catalogueMessage(locale, "retry")}
        </AppButton>
      </section>
    );
  return (
    <div className="mt-2.5 min-w-0">
      <LibraryArtistReleaseTracksView
        locale={locale}
        releaseTitle={releaseTitle}
        tracks={positions.tracks}
        releaseSelection={releaseSelection}
      />
      {positions.pagination && (
        <LibraryPaginationControls {...positions.pagination} />
      )}
    </div>
  );
}
