import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import {
  LibraryAlbumListView,
  type LibraryAlbumListViewProps,
} from "./LibraryAlbumListView";
import { LibraryArtistCards } from "./LibraryArtistCards";
import { LibraryTrackListView } from "./LibraryTrackListView";

/** Typed artist leaf model; the page supplies the locale. */
export type LibraryCatalogArtistView = ComponentType<
  ComponentProps<typeof LibraryArtistCards>
>;

export type LibraryCatalogArtistResults = Omit<
  ComponentProps<typeof LibraryArtistCards>,
  "locale"
> & {
  /**
   * Optional host view with the same props. Production supplies a wrapper that
   * binds artist photos; portable hosts keep the default card view.
   */
  View?: LibraryCatalogArtistView;
};

/** Typed track leaf model; the page supplies the locale. */
export type LibraryCatalogTrackResults = Omit<
  ComponentProps<typeof LibraryTrackListView>,
  "locale"
>;

/** Typed album leaf model; the page supplies the locale. */
export type LibraryCatalogAlbumModel = Omit<
  LibraryAlbumListViewProps,
  "locale"
>;

/** Stable host provider that owns album row state and reads. */
export type LibraryCatalogAlbumDataProvider = ComponentType<{
  children: ReactNode;
}>;

/**
 * Album results carry a stable data provider. The provider binds the typed
 * model here; the fixed list mount below renders it with the page locale.
 */
export type LibraryCatalogAlbumResults = {
  DataProvider: LibraryCatalogAlbumDataProvider;
};

/** Results selected by their typed leaf, not by a host-rendered tree. */
export type LibraryCatalogResults =
  | ({ kind: "artists" } & LibraryCatalogArtistResults)
  | ({ kind: "tracks" } & LibraryCatalogTrackResults)
  | ({ kind: "albums" } & LibraryCatalogAlbumResults);

const AlbumModelContext = createContext<LibraryCatalogAlbumModel | null>(null);

/** Host adapter: binds album list data without rendering the portable view. */
export function LibraryAlbumDataBinder({
  model,
  children,
}: {
  model: LibraryCatalogAlbumModel;
  children: ReactNode;
}) {
  return (
    <AlbumModelContext.Provider value={model}>
      {children}
    </AlbumModelContext.Provider>
  );
}

function BoundAlbumListView({ locale }: { locale: "en" | "ru" }) {
  const model = useContext(AlbumModelContext);
  if (!model) throw new Error("missing library album view model");
  return <LibraryAlbumListView locale={locale} {...model} />;
}

/**
 * Selects and mounts the typed result leaf. Artist and track models are
 * complete leaf props; album rows mount through the host data provider and the
 * fixed portable list below.
 */
export function LibraryCatalogResultView({
  locale,
  results,
}: {
  locale: "en" | "ru";
  results: LibraryCatalogResults;
}) {
  switch (results.kind) {
    case "artists": {
      const View = results.View ?? LibraryArtistCards;
      return (
        <View
          locale={locale}
          artists={results.artists}
          pageSelectionControl={results.pageSelectionControl}
        />
      );
    }
    case "tracks":
      return (
        <LibraryTrackListView
          locale={locale}
          rows={results.rows}
          pageSelection={results.pageSelection}
          label={results.label}
        />
      );
    case "albums": {
      const DataProvider = results.DataProvider;
      return (
        <DataProvider>
          <BoundAlbumListView locale={locale} />
        </DataProvider>
      );
    }
  }
}
