import type { LibraryPositionPage } from "../../api/generated/client.schemas";
import type { ArtistCardSelectionControl } from "../../ui/features/library/LibraryArtistCards";
import type { LibraryCatalogTrackResults } from "../../ui/features/library/LibraryCatalogResults";
import type { LibraryTrackRow } from "../../ui/features/library/LibraryTrackListView";
import {
  libraryPositionHref,
  libraryReleaseHref,
} from "../../ui/features/library/libraryDetailRoute";
import { libraryArtistRouteHref } from "./libraryRoute";
import type { LibrarySelectionScope } from "./useLibrarySelection";

type SelectionControl = (
  scope: LibrarySelectionScope,
) => ArtistCardSelectionControl;

type Position = LibraryPositionPage["items"][number];

const untitledTitle = { en: "Untitled", ru: "Без названия" } as const;

function positionCredits(position: Position): LibraryTrackRow["credits"] {
  return position.artists.map((credit) => ({
    name: credit.credit_name || credit.artist_name || "",
    href: credit.artist_id ? libraryArtistRouteHref(credit.artist_id) : null,
    joinPhrase: credit.join_phrase,
  }));
}

export type LibraryPositionRowsProps = {
  items: LibraryPositionPage["items"];
  selectionControl: SelectionControl;
};

/**
 * Pure track-row projection for the portable track leaf. The live screen and
 * tests can build the typed result without mounting the route.
 */
export function libraryPositionRowsModel({
  items,
  selectionControl,
  locale,
}: LibraryPositionRowsProps & {
  locale: "en" | "ru";
}): LibraryCatalogTrackResults {
  const rows: LibraryTrackRow[] = items.map((position) => {
    const title = position.title || untitledTitle[locale];
    return {
      id: position.id,
      title,
      durationMs: position.provider_duration_ms,
      href: libraryPositionHref(position.release.id, position.id),
      releaseTitle: position.release.title,
      releaseHref: libraryReleaseHref(position.release.id),
      credits: positionCredits(position),
      positionLabel: `${position.medium} · ${position.displayed_number || position.position}`,
      hasAudio: position.audio_available,
      variantCount: position.source_variant_count,
      published: Boolean(position.publication),
      selection: selectionControl({
        kind: "position",
        position: {
          release_id: position.release.id,
          position_id: position.id,
          title,
          release_title: position.release.title,
        },
      }),
    };
  });
  return {
    rows,
    pageSelection: selectionControl({ kind: "page" }),
  };
}
