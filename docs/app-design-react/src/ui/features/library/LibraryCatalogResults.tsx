import type { ComponentProps, ReactNode } from "react";
import { LibraryArtistCards } from "./LibraryArtistCards";
import { LibraryTrackListView } from "./LibraryTrackListView";

/** Typed artist leaf model; the page supplies the locale. */
export type LibraryCatalogArtistResults = Omit<
  ComponentProps<typeof LibraryArtistCards>,
  "locale"
>;

/** Typed track leaf model; the page supplies the locale. */
export type LibraryCatalogTrackResults = Omit<
  ComponentProps<typeof LibraryTrackListView>,
  "locale"
>;

/**
 * Narrow album-only temporary slot. Album rows keep their host-owned mount and
 * queries until a typed scoped provider replaces this slot.
 */
export type LibraryCatalogAlbumResults = {
  slot: ReactNode;
};

/** Results selected by their typed leaf, not by a host-rendered tree. */
export type LibraryCatalogResults =
  | ({ kind: "artists" } & LibraryCatalogArtistResults)
  | ({ kind: "tracks" } & LibraryCatalogTrackResults)
  | ({ kind: "albums" } & LibraryCatalogAlbumResults);

/**
 * Selects and mounts the typed result leaf. Artist and track models are
 * complete leaf props; album rows stay an explicit host-rendered slot.
 */
export function LibraryCatalogResultView({
  locale,
  results,
}: {
  locale: "en" | "ru";
  results: LibraryCatalogResults;
}) {
  switch (results.kind) {
    case "artists":
      return (
        <LibraryArtistCards
          locale={locale}
          artists={results.artists}
          pageSelectionControl={results.pageSelectionControl}
        />
      );
    case "tracks":
      return (
        <LibraryTrackListView
          locale={locale}
          rows={results.rows}
          pageSelection={results.pageSelection}
          label={results.label}
        />
      );
    case "albums":
      return <>{results.slot}</>;
  }
}
