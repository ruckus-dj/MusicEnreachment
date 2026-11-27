import { useEffect, useState } from "react";
import {
  useGetLibraryReleaseGroupEditionTracks,
  useListLibraryReleaseGroupEditions,
} from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryReleaseGroupEditionResponse,
  MissingReleaseGroupSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { catalogueMessage } from "../../ui/features/library/catalogueMessages";
import type {
  LibraryEditionPagination,
  LibraryEditionsStatus,
  LibraryEditionTracks,
  LibraryEditionTracksStatus,
  LibraryMissingEditionViewProps,
} from "../../ui/features/library/LibraryMissingEditionView";
import { isUuid } from "./libraryRoute";
import { providerReleaseFrontArtworkURL } from "./providerArtwork";

const EDITION_PAGE_SIZE = 50;

function creditNames(credits: LibraryArtistCreditResponse[]) {
  return credits
    .map((credit, index) => {
      const name = credit.credit_name || credit.artist_name || "";
      return `${name}${credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}`;
    })
    .join("");
}

function editionLabel(
  edition: LibraryReleaseGroupEditionResponse,
  unknownTitle: string,
) {
  const details = [
    creditNames(edition.artists),
    edition.edition.release_date ?? edition.edition.original_release_date,
    edition.edition.country,
    ...edition.edition.labels,
    ...edition.edition.catalog_numbers,
    edition.edition.disambiguation,
    edition.provider_key,
  ]
    .filter(Boolean)
    .join(" · ");
  return `${edition.title || unknownTitle}${details ? ` — ${details}` : ""}`;
}

function responseError(query: {
  data?: { status: number; data?: unknown };
  error?: unknown;
}): string | null {
  const response = query.data;
  if (response?.status !== undefined && response.status !== 200) {
    const body = response.data;
    return typeof body === "object" &&
      body !== null &&
      "detail" in body &&
      typeof body.detail === "string"
      ? body.detail
      : "";
  }
  return query.error instanceof Error ? query.error.message : null;
}

interface EditionSelectionState {
  groupKey: string;
  pickerOpen: boolean;
  editionOffset: number;
  selectedEdition: LibraryReleaseGroupEditionResponse | null;
}

function resetEditionSelection(
  groupKey: string,
  pickerOpen: boolean,
): EditionSelectionState {
  return { groupKey, pickerOpen, editionOffset: 0, selectedEdition: null };
}

/**
 * Headless MusicBrainz edition projection for one missing release group.
 * Editions load only while the picker is visible; the read-only track list is
 * fetched for the sole known edition on expansion or after an explicit
 * choice among multiple editions. An explicit choice stays visible when
 * the picker closes.
 */
export function useMissingReleaseGroupEditions(
  group: MissingReleaseGroupSummary | null,
  {
    enabled = true,
    initialPickerOpen = false,
  }: { enabled?: boolean; initialPickerOpen?: boolean } = {},
) {
  const { locale } = useI18n();
  const groupKey = group?.provider_key ?? "";
  const [state, setState] = useState<EditionSelectionState>(() =>
    resetEditionSelection(groupKey, initialPickerOpen),
  );
  const active =
    state.groupKey === groupKey
      ? state
      : resetEditionSelection(groupKey, initialPickerOpen);

  // A different group starts closed over the previous selection. Collapsing and
  // re-expanding also reaches the empty key, so a later expansion never reuses
  // an earlier expanded group's preview.
  useEffect(() => {
    if (state.groupKey !== groupKey) {
      setState(resetEditionSelection(groupKey, initialPickerOpen));
    }
  }, [groupKey, initialPickerOpen, state.groupKey]);

  function update(patch: Partial<Omit<EditionSelectionState, "groupKey">>) {
    setState((current) => {
      const base =
        current.groupKey === groupKey
          ? current
          : resetEditionSelection(groupKey, initialPickerOpen);
      return { ...base, ...patch, groupKey };
    });
  }

  const editionsQuery = useListLibraryReleaseGroupEditions(
    groupKey,
    { limit: EDITION_PAGE_SIZE, offset: active.editionOffset },
    { query: { enabled: enabled && group !== null && active.pickerOpen } },
  );
  const editions =
    editionsQuery.data?.status === 200 ? editionsQuery.data.data : undefined;
  const pageEditions = editions?.editions ?? [];
  const editionsError = responseError(editionsQuery);
  const soleEdition =
    editionsError === null && editions?.total === 1 && pageEditions.length === 1
      ? pageEditions[0]
      : undefined;
  const selectedEditionID =
    (soleEdition
      ? active.pickerOpen
        ? soleEdition.provider_key
        : undefined
      : active.selectedEdition?.provider_key) ?? "";
  const tracksQuery = useGetLibraryReleaseGroupEditionTracks(
    groupKey,
    selectedEditionID,
    {
      query: {
        enabled: enabled && group !== null && selectedEditionID !== "",
        queryKey: [
          "/api/library/release-groups",
          groupKey,
          "editions",
          selectedEditionID,
          "tracks",
        ],
      },
    },
  );
  const editionOptions =
    !soleEdition &&
    active.selectedEdition &&
    !pageEditions.some(
      (edition) =>
        edition.provider_key === active.selectedEdition?.provider_key,
    )
      ? [active.selectedEdition, ...pageEditions]
      : pageEditions;
  const tracksError = responseError(tracksQuery);
  const trackList =
    tracksQuery.data?.status === 200 ? tracksQuery.data.data : undefined;

  const editionsStatus: LibraryEditionsStatus = editionsQuery.isLoading
    ? "loading"
    : editionsError !== null
      ? "error"
      : editions?.total === 0
        ? "empty"
        : "ready";
  const tracksStatus: LibraryEditionTracksStatus = tracksQuery.isLoading
    ? "loading"
    : tracksError !== null
      ? "error"
      : "ready";
  const editionChoices = editionOptions.map((edition) => ({
    key: edition.provider_key,
    label: editionLabel(edition, catalogueMessage(locale, "unnamedEdition")),
    trackCount: edition.track_count,
    imageSrc: isUuid(edition.provider_key)
      ? providerReleaseFrontArtworkURL(edition.provider_key)
      : null,
  }));
  const tracks: LibraryEditionTracks | null =
    selectedEditionID && trackList
      ? {
          complete: trackList.complete,
          trackCount: trackList.track_count,
          tracks: trackList.tracks.map((track) => ({
            key: `${track.medium}:${track.position}`,
            title: track.title,
            positionLabel: `${track.medium}.${track.number}`,
            recordingTitle: track.recording_title,
            durationMs: track.duration_ms,
          })),
        }
      : null;
  const pagination: LibraryEditionPagination | null =
    editions && (active.editionOffset > 0 || editions.total > EDITION_PAGE_SIZE)
      ? {
          page: Math.floor(active.editionOffset / EDITION_PAGE_SIZE) + 1,
          hasPrevious: active.editionOffset > 0,
          hasNext: active.editionOffset + EDITION_PAGE_SIZE < editions.total,
          onPrevious: () =>
            update({
              editionOffset: Math.max(
                0,
                active.editionOffset - EDITION_PAGE_SIZE,
              ),
            }),
          onNext: () =>
            update({ editionOffset: active.editionOffset + EDITION_PAGE_SIZE }),
        }
      : null;
  const viewProps: Omit<LibraryMissingEditionViewProps, "locale"> = {
    pickerVisible: active.pickerOpen,
    soleEdition: Boolean(soleEdition),
    editions: editionChoices,
    editionsStatus,
    editionsError,
    onRetryEditions: () => void editionsQuery.refetch(),
    selectedEditionKey: selectedEditionID,
    onSelectEdition: (editionKey) =>
      update({
        selectedEdition:
          editionOptions.find(
            (edition) => edition.provider_key === editionKey,
          ) ?? null,
      }),
    pagination,
    tracksStatus,
    tracksError,
    onRetryTracks: () => void tracksQuery.refetch(),
    tracks,
  };

  return {
    locale,
    title: group?.title || catalogueMessage(locale, "unnamedReleaseGroup"),
    credits: creditNames(group?.artists ?? []),
    originalReleaseDate: group?.original_release_date ?? null,
    pickerOpen: active.pickerOpen,
    hasSoleEdition: Boolean(soleEdition),
    togglePicker: () => update({ pickerOpen: !active.pickerOpen }),
    showEditionView: active.pickerOpen || selectedEditionID !== "",
    viewProps,
  };
}
