import type { ComponentProps } from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { GenrePicker as LibraryGenrePicker } from "../../../../ui/features/library/GenrePicker";
import {
  type GenreMessages,
  genreMessages,
} from "../../../../ui/features/library/genreMessages";
import { metadataMessage } from "../../../../ui/features/library/metadata/metadataFieldMessages";
import { useI18n } from "../../demoPreferences";
import { genreCatalog, resolveCatalogGenreName } from "../../genres/catalogue";

export type GenreReference = { id?: string; name: string };
export type GenreOption = { id: string; name: string };

export type GenrePickerProps = {
  value: GenreReference[];
  onChange: (next: GenreReference[]) => void;
  readOnly?: boolean;
  loaded: boolean;
  onLoad?: () => void | Promise<void>;
  displayValue?: string;
  label?: string;
  options?: readonly GenreOption[];
  lifetime?: string;
};

type LibraryGenrePickerProps = ComponentProps<typeof LibraryGenrePicker>;

const manualGenrePrefix = "demo-manual:";

/** Stable display identity for names that have no catalog or provider ID. */
const manualGenreId = (name: string) =>
  `${manualGenrePrefix}${encodeURIComponent(name)}`;

const isManualGenreId = (id: string) => id.startsWith(manualGenrePrefix);

/**
 * Demo genre-picker adapter. It owns the fixture catalog, name resolution,
 * search ranking, selection retention and load state, then supplies the props
 * of the shared library picker, which owns the dialog, status, search,
 * selection and keyboard behavior.
 */
export function useGenrePickerDemoProvider({
  value,
  onChange,
  readOnly = false,
  loaded,
  onLoad,
  displayValue,
  label,
  options = genreCatalog,
  lifetime = "picker",
}: GenrePickerProps): LibraryGenrePickerProps {
  const { locale } = useI18n();
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const loadGeneration = useRef(0);
  useEffect(() => {
    setQuery("");
    setLoading(false);
    setLoadError(false);
    return () => {
      loadGeneration.current++;
    };
  }, [lifetime]);

  const catalog = useMemo(
    () => new Map(options.map((item) => [item.id, item])),
    [options],
  );
  const catalogId = useCallback(
    (item: GenreReference): string | undefined =>
      item.id ??
      options.find(
        (option) =>
          resolveCatalogGenreName(option) === resolveCatalogGenreName(item),
      )?.id,
    [options],
  );
  const catalogGenreId = useCallback(
    (item: GenreReference): string =>
      item.id ?? catalogId(item) ?? manualGenreId(item.name),
    [catalogId],
  );
  const pickerValue = useMemo(
    () =>
      value.map((item) => {
        const id = catalogGenreId(item);
        const catalogEntry = catalog.get(id);
        const name = catalogEntry
          ? resolveCatalogGenreName(catalogEntry)
          : resolveCatalogGenreName(item);
        // The shared picker shows its source name as secondary text. Demo
        // entries use the same resolved name so the prototype makes no claim
        // about a real provider origin.
        return { id, source_name: name, name };
      }),
    [catalog, catalogGenreId, value],
  );
  const originalById = useMemo(() => {
    const entries = new Map<string, GenreReference>();
    for (const item of value) {
      const id = catalogGenreId(item);
      if (!entries.has(id)) entries.set(id, item);
    }
    return entries;
  }, [catalogGenreId, value]);

  const search = query.trim().toLocaleLowerCase("en");
  const filtered = useMemo(() => {
    const matches = options.filter((item) =>
      item.name.toLocaleLowerCase("en").includes(search),
    );
    // Exact and prefix matches must not disappear behind a page of subgenres.
    if (!search) return matches;
    const rank = (name: string) => {
      const normalized = name.toLocaleLowerCase("en");
      return normalized === search ? 0 : normalized.startsWith(search) ? 1 : 2;
    };
    return matches.sort((left, right) => rank(left.name) - rank(right.name));
  }, [options, search]);
  const pickerOptions = useMemo(
    () =>
      filtered.map((item) => {
        const name = resolveCatalogGenreName(item);
        return { id: item.id, source_name: name, name };
      }),
    [filtered],
  );

  const changeSelection = useCallback(
    (next: LibraryGenrePickerProps["value"]) => {
      onChange(
        next.map((item) => {
          const original = originalById.get(item.id);
          if (original) return original;
          const catalogEntry = catalog.get(item.id);
          if (catalogEntry) {
            return {
              id: catalogEntry.id,
              name: resolveCatalogGenreName(catalogEntry),
            };
          }
          // Never persist a display-only synthetic identity.
          return isManualGenreId(item.id)
            ? { name: resolveCatalogGenreName(item) }
            : { id: item.id, name: resolveCatalogGenreName(item) };
        }),
      );
    },
    [catalog, onChange, originalById],
  );

  const load = async () => {
    if (!onLoad || loading) return;
    const generation = loadGeneration.current;
    setLoading(true);
    setLoadError(false);
    try {
      await onLoad();
    } catch {
      if (loadGeneration.current === generation) setLoadError(true);
    } finally {
      if (loadGeneration.current === generation) setLoading(false);
    }
  };

  // Keep the prototype copy while the shared picker owns presentation. The
  // metadata namespace has no wording for provider refresh states, so those
  // remain the shared picker's fallback copy.
  const copy: GenreMessages = {
    ...genreMessages[locale],
    emptyDisplay: displayValue || "—",
    available: metadataMessage(locale, "genre.available"),
    catalogEmpty: metadataMessage(locale, "genre.empty"),
    catalogError: "",
    catalogLoading: metadataMessage(locale, "state.loading"),
    catalogUnloaded: metadataMessage(locale, "genre.notLoaded"),
    choose: metadataMessage(locale, "genre.choose"),
    chooseDialog: metadataMessage(locale, "genre.chooseDialog"),
    done: metadataMessage(locale, "action.done"),
    loadBundledCatalog: metadataMessage(locale, "action.loadCatalog"),
    loading: metadataMessage(locale, "state.loading"),
    more: metadataMessage(locale, "genre.more"),
    noMatches: metadataMessage(locale, "genre.noMatches"),
    openSettings: metadataMessage(locale, "action.settings"),
    remove: metadataMessage(locale, "action.remove"),
    retry: metadataMessage(locale, "action.retry"),
    search: metadataMessage(locale, "genre.search"),
    searchPlaceholder: metadataMessage(locale, "genre.searchPlaceholder"),
    selected: metadataMessage(locale, "genre.selected"),
  };

  return {
    value: pickerValue,
    onChange: changeSelection,
    editing: !readOnly,
    label: label ?? metadataMessage(locale, "genre.label"),
    copy,
    disabled: readOnly,
    options: pickerOptions,
    query,
    onQueryChange: setQuery,
    loading: false,
    catalogError: "",
    onClearCatalogError: () => undefined,
    onRetryCatalog: () => undefined,
    catalogStatusLoading: loading,
    catalogStatusError:
      !loaded && loadError ? metadataMessage(locale, "genre.loadError") : "",
    bundledCatalogReady: loaded,
    onLoadBundledCatalog: onLoad ? () => void load() : undefined,
    settingsHref: "#/settings",
    onOpenCatalog: () => undefined,
    onRetryStatus: () => void load(),
  };
}
