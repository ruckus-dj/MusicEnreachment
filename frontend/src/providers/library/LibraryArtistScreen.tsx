import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  getGetLibraryReleaseCoverImageUrl,
  useGetLibraryArtist,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryReleaseSummary,
  MissingReleaseGroupSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { useHashLocation } from "../../app/navigation/hashLocation";
import {
  ApplicationLibraryArtistBinder,
  type ApplicationLibraryArtistModel,
  type ApplicationRouteProviderProps,
} from "../../ui/app/ApplicationRouteOutlet";
import { ArtistMissingGroupChooser } from "../../ui/features/library/ArtistMissingGroupChooserView";
import { ArtistReleasePositionsView } from "../../ui/features/library/ArtistReleasePositionsView";
import type {
  ArtistDeletionAction,
  ArtistEditionModel,
  ArtistHeadingModel,
  ArtistMissingGroupCredit,
  ArtistMissingGroupModel,
  ArtistReleaseRowModel,
  ArtistReleasesModel,
} from "../../ui/features/library/ArtistScreenView";
import { catalogueMessage } from "../../ui/features/library/catalogueMessages";
import type { LibraryStateFilter } from "../../ui/features/library/LibraryCatalogFilterView";
import { LibraryMissingEditionView } from "../../ui/features/library/LibraryMissingEditionView";
import {
  libraryPositionHref,
  libraryReleaseHref,
} from "../../ui/features/library/libraryDetailRoute";
import { LibraryArtistMetadataCard } from "./LibraryArtistMetadataCard";
import { LibraryDeletionControl } from "./LibraryDeletionControl";
import {
  type LibraryRoute,
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryArtistRoute,
} from "./libraryRoute";
import { useMissingReleaseGroupEditions } from "./MissingReleaseGroupRow";

const PAGE_SIZE = 50;
const SEARCH_DEBOUNCE_MS = 250;

function stateFilterFor(route: LibraryRoute): LibraryStateFilter {
  if (route.publication === "published") return "published";
  if (route.availability === "available") return "available";
  if (route.availability === "missing") return "missing";
  return "all";
}

function withStateFilter(
  route: LibraryRoute,
  filter: LibraryStateFilter,
): LibraryRoute {
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

function creditNames(credits: LibraryArtistCreditResponse[]) {
  return credits
    .map(
      (credit, index) =>
        `${credit.credit_name || credit.artist_name || ""}${credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}`,
    )
    .join("");
}

function creditLinks(
  credits: LibraryArtistCreditResponse[],
  unnamedArtist: string,
): ArtistMissingGroupCredit[] {
  return credits.map((credit) => ({
    name: credit.credit_name || credit.artist_name || unnamedArtist,
    href: credit.artist_id
      ? `#/library/artists/${encodeURIComponent(credit.artist_id)}`
      : null,
    joinPhrase: credit.join_phrase,
  }));
}

/**
 * Narrow edition chooser actor for one absent provider group. The MusicBrainz
 * cache is read only while the picker is open; the shell and row labels stay
 * in the screen view.
 */
function MissingGroupChooser({ group }: { group: MissingReleaseGroupSummary }) {
  const projection = useMissingReleaseGroupEditions(group);
  return (
    <ArtistMissingGroupChooser
      locale={projection.locale}
      isExpanded={projection.pickerOpen}
      onToggle={projection.togglePicker}
    >
      {projection.showEditionView && (
        <LibraryMissingEditionView
          locale={projection.locale}
          {...projection.viewProps}
          groupTitle={projection.title}
        />
      )}
    </ArtistMissingGroupChooser>
  );
}

export function ProductionLibraryArtistBinder({
  artistId,
  children,
}: { artistId: string } & ApplicationRouteProviderProps) {
  const model = useLibraryArtistModel(artistId);
  return (
    <ApplicationLibraryArtistBinder model={model}>
      {children}
    </ApplicationLibraryArtistBinder>
  );
}

function useLibraryArtistModel(
  artistId: string,
): ApplicationLibraryArtistModel {
  const { locale } = useI18n();
  const copy = (
    key: Parameters<typeof catalogueMessage>[1],
    values?: Record<string, string | number>,
  ) => catalogueMessage(locale, key, values);
  const queryClient = useQueryClient();
  const hash = useHashLocation();
  const route = parseLibraryArtistRoute(hash) ?? {
    artistId,
    view: "artists" as const,
    query: "",
    publication: "all" as const,
    availability: "all" as const,
    offset: 0,
  };
  const artistQuery = useGetLibraryArtist(artistId);
  const releasesQuery = useListLibraryReleases({
    artist_id: artistId,
    limit: PAGE_SIZE,
    offset: route.offset,
    q: route.query || undefined,
    publication: route.publication,
    availability: route.availability,
  });
  const [searchInput, setSearchInput] = useState(route.query);
  const searchTimer = useRef<number | null>(null);
  useEffect(() => {
    if (searchTimer.current !== null) {
      window.clearTimeout(searchTimer.current);
      searchTimer.current = null;
    }
    setSearchInput(parseLibraryArtistRoute(hash)?.query ?? route.query);
  }, [hash, route.query]);
  useEffect(
    () => () => {
      if (searchTimer.current !== null)
        window.clearTimeout(searchTimer.current);
    },
    [],
  );
  function currentRoute() {
    return parseLibraryArtistRoute(window.location.hash) ?? route;
  }
  function changeQuery(value: string) {
    setSearchInput(value);
    if (searchTimer.current !== null) window.clearTimeout(searchTimer.current);
    const capturedHash = window.location.hash;
    searchTimer.current = window.setTimeout(() => {
      searchTimer.current = null;
      if (window.location.hash !== capturedHash) return;
      const current = currentRoute();
      if (current.query === value) return;
      window.location.hash = libraryArtistRouteHref(artistId, {
        ...current,
        query: value,
        offset: 0,
      }).slice(1);
    }, SEARCH_DEBOUNCE_MS);
  }
  function changeFilter(value: LibraryStateFilter) {
    const current = currentRoute();
    if (stateFilterFor(current) === value) return;
    window.location.hash = libraryArtistRouteHref(artistId, {
      ...withStateFilter(current, value),
      offset: 0,
    }).slice(1);
  }
  function invalidateArtistMetadata() {
    void queryClient.invalidateQueries({
      predicate: ({ queryKey }) => {
        const key = queryKey[0];
        return (
          typeof key === "string" &&
          (key.startsWith("/api/library/artists") ||
            key.startsWith("/api/library/releases") ||
            key.includes("/publication/"))
        );
      },
    });
  }
  function invalidateDeletionCatalog() {
    void queryClient.invalidateQueries({
      predicate: ({ queryKey }) => {
        const key = queryKey[0];
        if (typeof key !== "string") return false;
        return (
          key === "/api/library/artists" ||
          key.startsWith("/api/library/artists?") ||
          key.startsWith("/api/library/releases") ||
          key.startsWith("/api/library/positions") ||
          key.includes("/publication/") ||
          key.includes("/deletion/preview")
        );
      },
    });
  }
  function handleArtistDeletionSnapshot(snapshot: { state: string }) {
    if (snapshot.state !== "failed" && snapshot.state !== "succeeded") return;
    invalidateDeletionCatalog();
    if (snapshot.state === "succeeded") {
      // Do not refetch the now-hidden artist detail: leave this route directly.
      window.location.hash = libraryRouteHref(route).slice(1);
    }
  }
  const error =
    artistQuery.data?.status !== undefined && artistQuery.data.status !== 200
      ? apiResponseError(
          artistQuery.data,
          copy("artistLoadFailed", { status: artistQuery.data.status }),
        ).message
      : artistQuery.error instanceof Error
        ? artistQuery.error.message
        : "";
  const artist =
    artistQuery.data?.status === 200 ? artistQuery.data.data : undefined;
  const releasesError =
    releasesQuery.data?.status !== undefined &&
    releasesQuery.data.status !== 200
      ? apiResponseError(
          releasesQuery.data,
          copy("releaseLoadFailed", { status: releasesQuery.data.status }),
        ).message
      : releasesQuery.error instanceof Error
        ? releasesQuery.error.message
        : "";
  const releases =
    releasesQuery.data?.status === 200 ? releasesQuery.data.data : undefined;

  if (!artist) {
    return {
      locale,
      libraryHref: libraryRouteHref(route),
      heading: null,
      status: artistQuery.isLoading
        ? { state: "loading", message: copy("loadingArtist") }
        : error
          ? {
              state: "error",
              message: error,
              retryLabel: copy("retry"),
              onRetry: () => void artistQuery.refetch(),
            }
          : undefined,
    };
  }

  const heading: ArtistHeadingModel = {
    name: artist.name,
    disambiguation: artist.disambiguation,
    genres: artist.genres,
    count: {
      releases: artist.release_count,
      positions: artist.position_count,
      published: artist.published_position_count,
      available: artist.available_position_count,
      missing: artist.missing_release_groups ?? 0,
      local: artist.release_count - (artist.missing_release_groups ?? 0),
    },
    includesCachedGroups: !artist.discography_complete,
    discographyComplete: artist.discography_complete,
  };
  const deletion: ArtistDeletionAction = {
    kind: "actor",
    node: (
      <LibraryDeletionControl
        target={{ type: "artist", artistId }}
        releaseTitle={artist.name}
        onSnapshot={handleArtistDeletionSnapshot}
      />
    ),
  };
  const metadataCard = (
    <LibraryArtistMetadataCard
      key={artistId}
      artistId={artistId}
      onSaved={invalidateArtistMetadata}
    />
  );

  function editionModel(release: LibraryReleaseSummary): ArtistEditionModel {
    const title = release.title.trim() ? release.title : copy("unnamedRelease");
    const date =
      release.edition.release_date ??
      release.edition.original_release_date ??
      copy("dateNotSpecified");
    const catalog = release.edition.catalog_numbers[0] ?? "";
    const generation = release.cover?.selected_generation;
    const provider = release.provider_links[0];
    return {
      key: release.id,
      title,
      titleHref: libraryReleaseHref(release.id),
      year: date,
      catalog,
      ariaLabel: `${title} · ${date} · ${catalog || "—"}`,
      artwork: {
        title,
        imageSrc: generation
          ? getGetLibraryReleaseCoverImageUrl(release.id, generation)
          : undefined,
      },
      providerText: provider
        ? `${provider.provider_code} · ${provider.provider_key}`
        : copy("localRelease"),
      positionCount: release.counts.position_count,
      availableCount: release.counts.available_position_count,
      publishedCount: release.counts.published_position_count,
      country: release.edition.country ?? "",
      label: release.edition.labels[0] ?? "",
      providerTrackCount: release.counts.provider_position_count,
      metadataChangedCount: 0,
      positions: (
        <ReleasePositions
          releaseId={release.id}
          releaseTitle={title}
          locale={locale}
        />
      ),
    };
  }

  function missingGroupModel(
    group: MissingReleaseGroupSummary,
  ): ArtistMissingGroupModel {
    return {
      key: group.provider_key,
      title: group.title.trim() ? group.title : copy("unnamedReleaseGroup"),
      artistName: creditNames(group.artists) || copy("unnamedArtist"),
      originalReleaseDate: group.original_release_date,
      providerKey: group.provider_key,
      artistCredits: creditLinks(group.artists, copy("unnamedArtist")),
      chooser: <MissingGroupChooser group={group} />,
    };
  }

  const rows: ArtistReleaseRowModel[] =
    releases?.items.flatMap((item): ArtistReleaseRowModel[] => {
      if (item.kind === "library_release" && item.library_release) {
        return [
          { kind: "edition", edition: editionModel(item.library_release) },
        ];
      }
      if (item.kind === "missing_release_group" && item.missing_release_group) {
        return [
          {
            kind: "missing-group",
            group: missingGroupModel(item.missing_release_group),
          },
        ];
      }
      return [];
    }) ?? [];
  const pagination =
    releases && (route.offset > 0 || releases.total > PAGE_SIZE)
      ? {
          label: copy("releasePages"),
          status: rows.length
            ? `${route.offset + 1}–${Math.min(route.offset + PAGE_SIZE, releases.total)} ${copy("of")} ${releases.total}`
            : `0 ${copy("of")} ${releases.total}`,
          previous: {
            label: copy("previous"),
            disabled: route.offset === 0,
            href: libraryArtistRouteHref(artistId, {
              ...route,
              offset: Math.max(0, route.offset - PAGE_SIZE),
            }),
          },
          next: {
            label: copy("next"),
            disabled: route.offset + PAGE_SIZE >= releases.total,
            href: libraryArtistRouteHref(artistId, {
              ...route,
              offset: route.offset + PAGE_SIZE,
            }),
          },
        }
      : null;
  const releaseState: ArtistReleasesModel = releasesQuery.isLoading
    ? { state: "loading", message: copy("loadingReleases") }
    : releasesError
      ? {
          state: "error",
          message: releasesError,
          retryLabel: copy("retry"),
          onRetry: () => void releasesQuery.refetch(),
        }
      : rows.length === 0
        ? {
            state: "empty",
            title: copy("noReleasesFound"),
            description:
              route.query ||
              route.publication !== "all" ||
              route.availability !== "all"
                ? copy("changeFilters")
                : copy("artistHasNoReleases"),
            pagination,
          }
        : {
            state: "ready",
            rows,
            pagination,
          };

  return {
    locale,
    libraryHref: libraryRouteHref(route),
    heading,
    deletion,
    metadataCard,
    query: searchInput,
    filter: stateFilterFor(route),
    onQueryChange: changeQuery,
    onFilterChange: changeFilter,
    editionsKey: artistId,
    releases: releaseState,
  };
}

function ReleasePositions({
  releaseId,
  releaseTitle,
  locale,
}: {
  releaseId: string;
  releaseTitle: string;
  locale: "en" | "ru";
}) {
  const copy = (
    key: Parameters<typeof catalogueMessage>[1],
    values?: Record<string, string | number>,
  ) => catalogueMessage(locale, key, values);
  const [offset, setOffset] = useState(0);
  const query = useListLibraryPositions({
    release_id: releaseId,
    limit: PAGE_SIZE,
    offset,
    publication: "all",
    availability: "all",
  });
  const failure =
    query.data?.status !== undefined && query.data.status !== 200
      ? apiResponseError(
          query.data,
          copy("positionLoadFailed", { status: query.data.status }),
        ).message
      : query.error instanceof Error
        ? query.error.message
        : "";
  const page = query.data?.status === 200 ? query.data.data : undefined;
  const positions = page?.items ?? [];
  // An incomplete page cannot prove that this is a single-medium edition.
  const showMedium =
    (page?.total ?? 0) > positions.length ||
    new Set(positions.map(({ medium }) => medium)).size > 1;
  return (
    <ArtistReleasePositionsView
      locale={locale}
      releaseTitle={releaseTitle}
      positions={
        query.isLoading
          ? { state: "loading" }
          : failure
            ? {
                state: "error",
                message: failure,
                onRetry: () => void query.refetch(),
              }
            : {
                state: "ready",
                tracks: positions.map((position) => {
                  const display =
                    position.displayed_number.trim() ||
                    String(position.position).padStart(2, "0");
                  // Numeric labels may repeat across media. Qualified labels (A1,
                  // DVD1-3, etc.) are already the intended display value.
                  const numberLabel =
                    showMedium && position.medium && /^\d+$/.test(display)
                      ? `${position.medium}.${display}`
                      : display;
                  return {
                    id: position.id,
                    numberLabel,
                    title: position.title,
                    href: libraryPositionHref(releaseId, position.id),
                    durationMs: position.provider_duration_ms,
                    hasAudio: position.audio_available,
                    sourceCount: position.source_variant_count,
                    isNewRecording: !position.recording_id,
                    publication: position.publication
                      ? {
                          state: "published",
                          codec: position.publication.audio?.codec,
                          container: position.publication.audio?.container,
                        }
                      : { state: "not-published" },
                  };
                }),
                pagination:
                  positions.length > 0 && page && page.total > PAGE_SIZE
                    ? {
                        label: copy("positionPages"),
                        status: `${offset + 1}–${Math.min(offset + PAGE_SIZE, page.total)} ${copy("of")} ${page.total}`,
                        previous: {
                          label: copy("previous"),
                          disabled: offset === 0,
                          onPress: () =>
                            setOffset(Math.max(0, offset - PAGE_SIZE)),
                        },
                        next: {
                          label: copy("next"),
                          disabled: offset + PAGE_SIZE >= page.total,
                          onPress: () => setOffset(offset + PAGE_SIZE),
                        },
                      }
                    : undefined,
              }
      }
    />
  );
}
