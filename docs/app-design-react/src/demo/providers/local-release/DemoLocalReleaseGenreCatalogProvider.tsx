import { createContext, type ReactNode, useContext } from "react";
import type { GenrePickerCatalog } from "../../../ui/features/library/GenrePicker";
import {
  LocalReleaseGenreCatalogBinder,
  type LocalReleaseGenreCatalogProviderProps,
  type LocalReleaseGenreSelectionAdapter,
} from "../../../ui/features/local-release/LocalReleaseGenreCatalogOutlet";
import { resolveCatalogGenreName } from "../genres/catalogue";
import { useGenrePickerDemoProvider } from "../library/metadata/useGenrePickerDemoProvider";

type DemoLocalReleaseGenreLoad = {
  loaded: boolean;
  onLoad?: () => void | Promise<void>;
};

const DemoLocalReleaseGenreLoadContext =
  createContext<DemoLocalReleaseGenreLoad | null>(null);

/** Binds the workspace's fixture load state to the mounted genre editor. */
export function DemoLocalReleaseGenreLoadProvider({
  loaded,
  onLoad,
  children,
}: DemoLocalReleaseGenreLoad & { children: ReactNode }) {
  return (
    <DemoLocalReleaseGenreLoadContext.Provider value={{ loaded, onLoad }}>
      {children}
    </DemoLocalReleaseGenreLoadContext.Provider>
  );
}

const selection: LocalReleaseGenreSelectionAdapter = {
  display: (value) =>
    value.map((item) => {
      const name = resolveCatalogGenreName(item);
      return { id: item.id, name, source_name: name };
    }),
  adapt: (next, current) => {
    const currentById = new Map(current.map((item) => [item.id, item]));
    return next.map(
      (item) =>
        currentById.get(item.id) ?? {
          id: item.id,
          name: item.name,
          source_name: item.source_name,
        },
    );
  },
};

/**
 * Demo data provider for the incoming Local release genre editor. It is mounted
 * with the portable edit form, so the fixture picker's query state lives only
 * for that editor instance.
 */
export function DemoLocalReleaseGenreCatalogProvider({
  children,
}: LocalReleaseGenreCatalogProviderProps) {
  const load = useContext(DemoLocalReleaseGenreLoadContext);
  const catalog: GenrePickerCatalog = useGenrePickerDemoProvider({
    value: [],
    onChange: () => undefined,
    loaded: load?.loaded ?? false,
    onLoad: load?.onLoad,
  });
  if (!load) throw new Error("missing demo Local release genre load state");
  return (
    <LocalReleaseGenreCatalogBinder catalog={catalog} selection={selection}>
      {children}
    </LocalReleaseGenreCatalogBinder>
  );
}
