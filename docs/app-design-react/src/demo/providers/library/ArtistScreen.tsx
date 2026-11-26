import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApplicationLibraryArtistBinder,
  type ApplicationLibraryArtistModel,
  type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import { navigateHash, useHashLocation } from "../../../ui/app/hashLocation";
import {
  ArtistMetadataCard,
  type ArtistMetadataValues,
} from "../../../ui/features/library/ArtistMetadataCard";
import type {
  ArtistDeletionAction,
  ArtistEditionModel,
  ArtistHeadingModel,
  ArtistMissingGroupModel,
  ArtistReleaseRowModel,
  ArtistReleasesModel,
} from "../../../ui/features/library/ArtistScreenView";
import {
  type CatalogueMessage,
  catalogueMessage,
} from "../../../ui/features/library/catalogueMessages";
import {
  type LibraryArtistAddress,
  libraryArtistAddressHref,
  libraryPositionHref,
  parseLibraryArtistAddress,
} from "../../../ui/features/library/libraryDetailRoute";
import { useI18n } from "../demoPreferences";
import { genreCatalog as demoGenres } from "../genres/catalogue";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
import { releaseHref } from "./artistReleaseRoute";
import {
  availabilityRowClass,
  type CatalogFilter as CatalogFilterValue,
} from "./availabilityRows";
import {
  type CatalogArtist,
  catalogData,
  metadataChanged,
  type OriginalRelease,
  type OriginalTrack,
  originalArtistName,
  originalProviders,
  type SourceFiles,
  trackNumberLabel,
} from "./catalogData";
import {
  missingCatalogGroupsForAxes,
  normalizeCatalogSearch,
  releaseMatchesFilterAxes,
} from "./catalogViewModel";
import type { DeletionOperation } from "./DeletionRequest";
import { LibraryMissingEditionDemo } from "./LibraryMissingEditionDemo";
import { LIBRARY_PAGE_SIZE, libraryRootHref } from "./libraryRoute";
import { audioProperties } from "./metadata/audioProperties";

const libraryHref = libraryRootHref();
const SEARCH_DEBOUNCE_MS = 250;

function filterForRoute(route: LibraryArtistAddress): CatalogFilterValue {
  if (route.publication === "published") return "published";
  if (route.availability === "available") return "available";
  if (route.availability === "missing") return "missing";
  return "all";
}

function routeWithFilter(
  route: LibraryArtistAddress,
  filter: CatalogFilterValue,
): LibraryArtistAddress {
  return {
    ...route,
    publication: filter === "published" ? "published" : "all",
    availability:
      filter === "available"
        ? "available"
        : filter === "missing"
          ? "missing"
          : "all",
  };
}

const releaseFields = (release: OriginalRelease) =>
  release.credits?.some((credit) => credit.artistId) ?? false;

/** Live edition search matches title, release artists/credits and catalogue. */
function editionMatchesQuery(
  release: OriginalRelease,
  artists: CatalogArtist[],
  query: string,
) {
  const normalizedQuery = normalizeCatalogSearch(query);
  if (!normalizedQuery) return true;
  const creditedIds = new Set<string>();
  if (release.artistId) creditedIds.add(release.artistId);
  for (const credit of release.credits ?? [])
    if (credit.artistId) creditedIds.add(credit.artistId);
  const names = [
    ...(release.credits ?? []).flatMap((credit) => [
      credit.credit,
      credit.name,
    ]),
    ...[...creditedIds].map(
      (id) => artists.find((artist) => artist.id === id)?.name,
    ),
  ].filter((name): name is string => Boolean(name));
  return [release.title, ...names, release.catalog].some((value) =>
    normalizeCatalogSearch(value).includes(normalizedQuery),
  );
}

/** Live missing-group search matches release-group title and artist names. */
function missingGroupMatchesQuery(
  group: MissingGroup,
  artists: CatalogArtist[],
  query: string,
) {
  const normalizedQuery = normalizeCatalogSearch(query);
  if (!normalizedQuery) return true;
  const artistName = artists.find(
    (artist) => artist.id === group.artistId,
  )?.name;
  return [group.title, artistName ?? ""].some((value) =>
    normalizeCatalogSearch(value).includes(normalizedQuery),
  );
}

function genreOptions(query: string) {
  const term = query.trim().toLocaleLowerCase("en");
  if (!term) return [];
  const exact = [];
  const partial = [];
  for (const { id, name, sourceName } of demoGenres) {
    const fields = [id, name, sourceName].map((item) =>
      item.toLocaleLowerCase("en"),
    );
    const option = { id, name, source_name: sourceName };
    if (fields.includes(term)) exact.push(option);
    else if (fields.some((field) => field.includes(term))) partial.push(option);
  }
  return [...exact, ...partial];
}

export function ArtistScreen({
  artistId,
  artists,
  releases,
  files,
  selected = new Set(),
  onSelect,
  onDeleteArtist,
  onQueueDeletion,
  onUpdateArtist,
  genresLoaded = false,
  onLoadGenres,
  children,
}: {
  artistId: string;
  artists: CatalogArtist[];
  releases: OriginalRelease[];
  files: SourceFiles;
  selected?: Set<string>;
  onSelect?: (ids: string[], value: boolean) => void;
  onDeleteArtist?: (artistId: string) => void;
  onQueueDeletion?: (operation: DeletionOperation) => void;
  onUpdateArtist?: (artist: CatalogArtist) => void;
  genresLoaded?: boolean;
  onLoadGenres?: () => void | Promise<void>;
} & ApplicationRouteProviderProps) {
  const { locale } = useI18n();
  const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
    catalogueMessage(locale, key, values);
  const hash = useHashLocation();
  const parsedRoute = parseLibraryArtistAddress(hash);
  const route: LibraryArtistAddress =
    parsedRoute?.artistId === artistId
      ? parsedRoute
      : {
          artistId,
          view: "artists",
          query: "",
          publication: "all",
          availability: "all",
          offset: 0,
        };
  const [searchInput, setSearchInput] = useState(route.query);
  const pendingSearch = useRef<number | null>(null);
  const filter = filterForRoute(route);
  const [genreQuery, setGenreQuery] = useState("");
  const [localGenresLoaded, setLocalGenresLoaded] = useState(false);
  const genreSearchResults = useMemo(
    () => genreOptions(genreQuery),
    [genreQuery],
  );
  useEffect(() => {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
    setSearchInput(route.query);
  }, [hash, route.query]);
  useEffect(
    () => () => {
      if (pendingSearch.current !== null)
        window.clearTimeout(pendingSearch.current);
    },
    [],
  );
  function currentRoute() {
    const parsed = parseLibraryArtistAddress(window.location.hash);
    return parsed?.artistId === artistId ? parsed : route;
  }
  function changeQuery(value: string) {
    setSearchInput(value);
    if (pendingSearch.current !== null)
      window.clearTimeout(pendingSearch.current);
    const capturedHash = window.location.hash;
    pendingSearch.current = window.setTimeout(() => {
      pendingSearch.current = null;
      if (window.location.hash !== capturedHash) return;
      const current = currentRoute();
      if (current.query === value) return;
      navigateHash(
        libraryArtistAddressHref(artistId, {
          ...current,
          query: value,
          offset: 0,
        }),
      );
    }, SEARCH_DEBOUNCE_MS);
  }
  function changeFilter(value: CatalogFilterValue) {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
    const current = currentRoute();
    navigateHash(
      libraryArtistAddressHref(artistId, {
        ...routeWithFilter(current, value),
        query: searchInput,
        offset: 0,
      }),
    );
  }
  /** Clears search and both filter axes in one canonical navigation. */
  function resetFilters() {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
    setSearchInput("");
    const current = currentRoute();
    if (
      current.query === "" &&
      current.publication === "all" &&
      current.availability === "all" &&
      current.offset === 0
    )
      return;
    navigateHash(
      libraryArtistAddressHref(artistId, {
        ...current,
        query: "",
        publication: "all",
        availability: "all",
        offset: 0,
      }),
    );
  }
  function changePublication(value: LibraryArtistAddress["publication"]) {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
    const current = currentRoute();
    navigateHash(
      libraryArtistAddressHref(artistId, {
        ...current,
        publication: value,
        query: searchInput,
        offset: 0,
      }),
    );
  }
  function changeAvailability(value: LibraryArtistAddress["availability"]) {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
    const current = currentRoute();
    navigateHash(
      libraryArtistAddressHref(artistId, {
        ...current,
        availability: value,
        query: searchInput,
        offset: 0,
      }),
    );
  }
  const [localArtistUpdates, setLocalArtistUpdates] = useState<
    Record<string, CatalogArtist>
  >({});
  const artist =
    localArtistUpdates[artistId] ??
    artists.find((item) => item.id === artistId);
  const [absentEdition, setAbsentEdition] = useState<Record<string, string>>(
    {},
  );
  const artistReleases = useMemo(
    () =>
      releases.filter(
        (release) =>
          release.artistId === artistId ||
          (releaseFields(release) &&
            release.credits?.some((credit) => credit.artistId === artistId)),
      ),
    [artistId, releases],
  );
  const tracks = artistReleases.flatMap((release) => release.tracks);
  const allReleases = releases;
  const ids = tracks.map((track) => track.id);
  const hasAudio = (track: OriginalTrack) =>
    Boolean(track.published) ||
    [
      ...new Set([
        ...(track.sourceIds ?? []),
        ...(track.sourceId ? [track.sourceId] : []),
      ]),
    ].some((id) => files[id]?.available && files[id]?.locations?.length);
  const selectedCount = ids.filter((id) => selected.has(id)).length;
  const providerFor = (release: OriginalRelease) =>
    originalProviders.find((item) => item.id === release.providerId);
  const providerComplete = (release: OriginalRelease) => {
    const provider = providerFor(release);
    return Boolean(
      provider?.tracks.length &&
        provider.tracks.length === release.tracks.length,
    );
  };
  const filterAxes = {
    publication: route.publication,
    availability: route.availability,
  };
  const visibleEditions = artistReleases.filter(
    (release) =>
      editionMatchesQuery(release, artists, route.query) &&
      releaseMatchesFilterAxes(release.tracks, filterAxes, hasAudio),
  );
  const missingGroups = catalogData.absentGroups as MissingGroup[];
  const unrepresentedMissingGroups = missingGroups.filter(
    (group) =>
      group.artistId === artistId &&
      !allReleases.some(
        (release) =>
          (release as OriginalRelease & { releaseGroupId?: string })
            .releaseGroupId === group.groupId,
      ),
  );
  const visibleMissingGroups = missingCatalogGroupsForAxes(
    unrepresentedMissingGroups,
    allReleases,
    artists,
    "",
    filterAxes,
  ).filter((group) => missingGroupMatchesQuery(group, artists, route.query));
  if (!artist)
    return (
      <ApplicationLibraryArtistBinder
        model={{ locale, libraryHref, heading: null }}
      >
        {children}
      </ApplicationLibraryArtistBinder>
    );
  const counts = (predicate: (track: OriginalTrack) => boolean) =>
    tracks.filter(predicate).length;
  const selectedGenres = (artist.genres ?? []).flatMap((genre) => {
    if (!genre.id) return [];
    const known = demoGenres.find((item) => item.id === genre.id);
    const name = known?.name ?? genre.name ?? genre.id;
    return [{ id: genre.id, name, source_name: known?.sourceName ?? name }];
  });
  const saveArtist = async (changes: Partial<ArtistMetadataValues>) => {
    const genre = changes.genre;
    const inherited =
      artist.genreOverride?.inherited ??
      (artist.genres ?? []).filter((item) => !item.id);
    const updated = {
      ...artist,
      ...(changes.name !== undefined ? { name: changes.name } : {}),
      ...(changes.sortName !== undefined
        ? { sortName: changes.sortName ?? undefined }
        : {}),
      ...(changes.disambiguation !== undefined
        ? { disambiguation: changes.disambiguation ?? "" }
        : {}),
      ...(changes.tags !== undefined
        ? {
            tags: changes.tags
              .split(",")
              .map((tag) => tag.trim())
              .filter(Boolean),
          }
        : {}),
      ...(genre
        ? {
            genreOverride: { state: genre.state, inherited },
            genres:
              genre.state === "explicit_empty"
                ? []
                : [
                    ...genre.value.map(({ id, name }) => ({ id, name })),
                    ...inherited,
                  ],
          }
        : {}),
    };
    if (onUpdateArtist) onUpdateArtist(updated);
    else
      setLocalArtistUpdates((current) => ({
        ...current,
        [artist.id]: updated,
      }));
  };
  const metadataValues: ArtistMetadataValues = {
    name: artist.name,
    sortName: artist.sortName ?? null,
    disambiguation: artist.disambiguation ?? null,
    tags: artist.tags?.join(", ") ?? "",
    genre: {
      state: selectedGenres.length
        ? "value"
        : (artist.genreOverride?.state ?? "absent"),
      value: selectedGenres,
    },
  };
  const heading: ArtistHeadingModel = {
    name: artist.name,
    disambiguation: artist.disambiguation,
    genres: artist.genres?.some((genre) => genre.name)
      ? artist.genres.flatMap((genre) => (genre.name ? [genre.name] : []))
      : artist.genreOverride?.state !== "explicit_empty" && artist.genre
        ? [artist.genre]
        : [],
    palette: artist.color,
    count: {
      releases: artistReleases.length + unrepresentedMissingGroups.length,
      positions: tracks.length,
      published: counts((track) => Boolean(track.published)),
      missing: unrepresentedMissingGroups.length,
      local: artistReleases.length,
    },
    discographyComplete: artist.discographyComplete,
    includesCachedGroups: !artist.discographyComplete,
    selection: {
      total: ids.length,
      selected: selectedCount,
      onChange: onSelect && ((value) => onSelect(ids, value)),
    },
  };
  const deletion: ArtistDeletionAction = onQueueDeletion
    ? {
        kind: "actor",
        node: (
          <ArtistDeletionRequest
            artistId={artist.id}
            artists={artists}
            releases={releases}
            onQueue={onQueueDeletion}
          />
        ),
      }
    : {
        kind: "button",
        isDisabled: !onDeleteArtist,
        onPress: onDeleteArtist ? () => onDeleteArtist(artist.id) : undefined,
      };
  const metadataCard = (
    <ArtistMetadataCard
      key={artist.id}
      locale={locale}
      revision={artist.id}
      values={metadataValues}
      readOnly={[
        { id: "id", label: m("artistID"), value: artist.id },
        ...(
          (
            artist as CatalogArtist & {
              providerLinks?: {
                providerCode: string;
                providerKey: string;
              }[];
            }
          ).providerLinks ?? []
        ).map((link, index) => ({
          id: `provider-${link.providerCode}-${index}`,
          label: `ID ${link.providerCode}`,
          value: link.providerKey,
        })),
      ]}
      genreCaption={
        artist.genres
          ?.map((genre) => genre.name)
          .filter(Boolean)
          .join(", ") || artist.genre
      }
      genreCatalog={{
        options: genreSearchResults,
        query: genreQuery,
        onQueryChange: setGenreQuery,
        loading: false,
        catalogError: "",
        onClearCatalogError: () => undefined,
        onRetryCatalog: () => undefined,
        catalogStatusLoading: false,
        catalogStatusError: "",
        bundledCatalogReady: genresLoaded || localGenresLoaded,
        onLoadBundledCatalog: () => {
          if (onLoadGenres) void onLoadGenres();
          else setLocalGenresLoaded(true);
        },
        settingsHref: "#/settings",
        onOpenCatalog: () => undefined,
        onRetryStatus: () => undefined,
      }}
      onSave={saveArtist}
    />
  );
  const editions: ArtistEditionModel[] = visibleEditions.map((release) => {
    const durationTotal =
      release.tracks.length > 0 &&
      release.tracks.every((track) => Number.isFinite(track.duration))
        ? release.tracks.reduce((sum, track) => sum + (track.duration ?? 0), 0)
        : null;
    const pubCount = release.tracks.filter((track) => track.published).length;
    const provider = providerFor(release);
    const complete = providerComplete(release);
    const changes = release.tracks.filter((track) =>
      metadataChanged(release, track, artists),
    ).length;
    const trackIds = release.tracks.map((track) => track.id);
    const everySelected = release.tracks.every((track) =>
      selected.has(track.id),
    );
    const someSelected = release.tracks.some((track) => selected.has(track.id));
    const ariaLabel = `${release.title} · ${release.year} · ${release.catalog || "—"}`;
    const selectTracks = (value: boolean) => onSelect?.(trackIds, value);
    return {
      key: release.id,
      title: release.title,
      titleHref: releaseHref(release.id),
      year: release.year,
      catalog: release.catalog,
      ariaLabel,
      artwork: {
        palette: release.color || artist.color,
        title: release.title,
      },
      durationSeconds: durationTotal,
      providerText:
        release.provider === "Local"
          ? "Local"
          : release.providerId
            ? `${release.provider || "MusicBrainz"} · ${release.providerId}`
            : m("localRelease"),
      positionCount: release.tracks.length,
      country: release.country,
      label: release.label,
      publishedCount: pubCount,
      providerTrackCount: complete && provider ? provider.tracks.length : null,
      metadataChangedCount: changes,
      selection: {
        isSelected: release.tracks.length > 0 && everySelected,
        isIndeterminate: someSelected && !everySelected,
        isDisabled: !release.tracks.length || !onSelect,
        onChange: selectTracks,
      },
      trackSelection: {
        isSelected: everySelected,
        isIndeterminate: someSelected && !everySelected,
        isDisabled: !onSelect,
        onChange: selectTracks,
      },
      tracks: release.tracks.map((track) => ({
        id: track.id,
        numberLabel: trackNumberLabel(release, track),
        title: track.title,
        href: libraryPositionHref(release.id, track.id),
        durationMs: track.duration == null ? null : track.duration * 1000,
        properties: audioProperties(
          track.sourceId ? files[track.sourceId] : undefined,
        ),
        hasAudio: hasAudio(track),
        sourceCount: new Set([
          ...(track.sourceIds ?? []),
          ...(track.sourceId ? [track.sourceId] : []),
        ]).size,
        publication: track.published
          ? {
              state: "published" as const,
              container: track.published.container,
              codec: track.published.codec,
            }
          : { state: "not-published" as const },
        isNewRecording: !track.recordingId,
        fileEvidence: track.observedFile ?? undefined,
        metadataChanged: track.published
          ? metadataChanged(release, track, artists)
          : false,
        filterClass: availabilityRowClass(track, filter, hasAudio),
        selection: {
          isSelected: selected.has(track.id),
          isDisabled: !onSelect,
          onChange: (value: boolean) => onSelect?.([track.id], value),
        },
      })),
    };
  });
  const preparedMissingGroups: ArtistMissingGroupModel[] =
    visibleMissingGroups.map((group) => ({
      key: group.id,
      title: group.title,
      artistName: originalArtistName(group.artistId, artists),
      chooser: (
        <LibraryMissingEditionDemo
          key={group.id}
          locale={locale}
          groupTitle={group.title}
          editions={group.editions}
          selectedEditionKey={absentEdition[group.id] ?? ""}
          onSelectEdition={(editionKey) =>
            setAbsentEdition((value) => ({
              ...value,
              [group.id]: editionKey,
            }))
          }
        />
      ),
    }));
  const releaseRows: ArtistReleaseRowModel[] = [
    ...editions.map((edition) => ({ kind: "edition" as const, edition })),
    ...preparedMissingGroups.map((group) => ({
      kind: "missing-group" as const,
      group,
    })),
  ].sort((a, b) => {
    const aTitle = a.kind === "edition" ? a.edition.title : a.group.title;
    const bTitle = b.kind === "edition" ? b.edition.title : b.group.title;
    const normalizedA = normalizeCatalogSearch(aTitle);
    const normalizedB = normalizeCatalogSearch(bTitle);
    if (normalizedA !== normalizedB) return normalizedA < normalizedB ? -1 : 1;
    if (a.kind !== b.kind) return a.kind === "edition" ? -1 : 1;
    const aKey = a.kind === "edition" ? a.edition.key : a.group.key;
    const bKey = b.kind === "edition" ? b.edition.key : b.group.key;
    if (aKey === bKey) return 0;
    return aKey < bKey ? -1 : 1;
  });
  const pageRows = releaseRows.slice(
    route.offset,
    route.offset + LIBRARY_PAGE_SIZE,
  );
  const pagination =
    releaseRows.length > LIBRARY_PAGE_SIZE || route.offset > 0
      ? {
          label: m("releasePages"),
          status: pageRows.length
            ? `${route.offset + 1}–${Math.min(route.offset + LIBRARY_PAGE_SIZE, releaseRows.length)} ${m("of")} ${releaseRows.length}`
            : `0 ${m("of")} ${releaseRows.length}`,
          previous: {
            label: m("previous"),
            disabled: route.offset === 0,
            href: libraryArtistAddressHref(artistId, {
              ...route,
              offset: Math.max(0, route.offset - LIBRARY_PAGE_SIZE),
            }),
          },
          next: {
            label: m("next"),
            disabled: route.offset + LIBRARY_PAGE_SIZE >= releaseRows.length,
            href: libraryArtistAddressHref(artistId, {
              ...route,
              offset: route.offset + LIBRARY_PAGE_SIZE,
            }),
          },
        }
      : null;
  const hasFilter =
    route.query !== "" ||
    route.publication !== "all" ||
    route.availability !== "all";
  const releaseState: ArtistReleasesModel =
    pageRows.length === 0
      ? {
          state: "empty",
          title: m("noReleasesFound"),
          description: hasFilter
            ? m("changeFilters")
            : m("artistHasNoReleases"),
          pagination,
        }
      : { state: "ready", rows: pageRows, pagination };
  const model: ApplicationLibraryArtistModel = {
    locale,
    libraryHref,
    heading,
    deletion,
    metadataCard,
    query: searchInput,
    filter,
    onQueryChange: changeQuery,
    onFilterChange: changeFilter,
    publication: route.publication,
    availability: route.availability,
    onPublicationChange: changePublication,
    onAvailabilityChange: changeAvailability,
    onResetFilters: resetFilters,
    editionsKey: artistId,
    releases: releaseState,
  };
  return (
    <ApplicationLibraryArtistBinder model={model}>
      {children}
    </ApplicationLibraryArtistBinder>
  );
}

type MissingGroup = {
  id: string;
  groupId: string;
  artistId: string;
  title: string;
  editions: {
    id: string;
    year: string;
    country: string;
    format: string;
    disambiguation: string;
    titles: string[];
  }[];
};
