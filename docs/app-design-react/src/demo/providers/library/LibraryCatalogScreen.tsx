import {
  ApplicationLibraryCatalogBinder,
  type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import type { LibraryCatalogPageViewProps } from "../../../ui/features/library/LibraryCatalogPageView";
import {
  DemoLibraryAlbumRowsDataProvider,
  type LibraryCatalogAlbumRowsData,
} from "./LibraryCatalogRows";

export interface DemoLibraryCatalogProviderProps
  extends LibraryCatalogPageViewProps {
  /** Raw album controller data for the portable album results leaf. */
  albumRows?: LibraryCatalogAlbumRowsData | null;
}

/** Binds demo library catalog props to the portable route. */
export function DemoLibraryCatalogBinder({
  children,
  albumRows,
  ...model
}: DemoLibraryCatalogProviderProps & ApplicationRouteProviderProps) {
  return (
    <DemoLibraryAlbumRowsDataProvider value={albumRows ?? null}>
      <ApplicationLibraryCatalogBinder model={model}>
        {children}
      </ApplicationLibraryCatalogBinder>
    </DemoLibraryAlbumRowsDataProvider>
  );
}
