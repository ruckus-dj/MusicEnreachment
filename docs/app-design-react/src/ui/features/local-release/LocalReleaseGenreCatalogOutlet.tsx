import { createContext, type ReactNode, useContext } from "react";
import {
  GenrePicker,
  type GenrePickerCatalog,
  type GenreReference,
} from "../library/GenrePicker";
import { genreMessages } from "../library/genreMessages";
import type { LocalReleaseGenreFieldProps } from "./LocalReleaseForm";
import type { LocalReleaseGenre } from "./localReleaseFormModel";

/**
 * Optional host projection for stored draft rows. The demo host uses it to
 * resolve fallback catalog labels and to preserve exact stored values when
 * another genre is toggled; live keeps the unprojected picker behavior.
 */
export type LocalReleaseGenreSelectionAdapter = {
  display?: (value: readonly LocalReleaseGenre[]) => GenreReference[];
  adapt?: (
    next: readonly GenreReference[],
    current: readonly LocalReleaseGenre[],
  ) => LocalReleaseGenre[];
};

type LocalReleaseGenreCatalogBinding = {
  catalog: GenrePickerCatalog;
  selection?: LocalReleaseGenreSelectionAdapter;
};

const LocalReleaseGenreCatalogContext =
  createContext<LocalReleaseGenreCatalogBinding | null>(null);

/** Host-facing data shape for a stable Local release genre catalog provider. */
export type LocalReleaseGenreCatalogProviderProps = {
  children: ReactNode;
  /** Demo hosts may bind an already-built model instead of a live hook. */
  model?: GenrePickerCatalog;
  /** Demo-only selection projection; live leaves this unset. */
  selection?: LocalReleaseGenreSelectionAdapter;
};

/** Binds one genre catalog lifetime to the portable genre leaf below. */
export function LocalReleaseGenreCatalogBinder({
  catalog,
  selection,
  children,
}: {
  catalog: GenrePickerCatalog;
  selection?: LocalReleaseGenreSelectionAdapter;
  children: ReactNode;
}) {
  return (
    <LocalReleaseGenreCatalogContext.Provider value={{ catalog, selection }}>
      {children}
    </LocalReleaseGenreCatalogContext.Provider>
  );
}

/**
 * Shared Local release genre leaf. The catalog and its actions come from the
 * mounted host provider; this view keeps the canonical release-form labels.
 */
export function LocalReleaseGenreCatalogOutlet({
  locale,
  value,
  onChange,
  disabled,
}: LocalReleaseGenreFieldProps) {
  const binding = useContext(LocalReleaseGenreCatalogContext);
  if (!binding) throw new Error("missing Local release genre catalog");
  const copy = genreMessages[locale];
  const displayed = binding.selection?.display
    ? binding.selection.display(value)
    : value;
  const adapt = binding.selection?.adapt;
  const change = adapt
    ? (next: GenreReference[]) => onChange(adapt(next, value))
    : onChange;
  return (
    <GenrePicker
      {...binding.catalog}
      value={[...displayed]}
      onChange={change}
      editing
      label={copy.label.release}
      copy={copy}
      disabled={Boolean(disabled)}
    />
  );
}
