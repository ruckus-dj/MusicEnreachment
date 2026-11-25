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
import { AppButton } from "../../ui/components/AppButton";
import {
  type LibraryEditionPagination,
  type LibraryEditionsStatus,
  type LibraryEditionTracks,
  type LibraryEditionTracksStatus,
  LibraryMissingEditionView,
  type LibraryMissingEditionViewProps,
} from "../../ui/features/library/LibraryMissingEditionView";

const EDITION_PAGE_SIZE = 50;

const messages = {
  en: {
    artist: "Artist",
    dateUnknown: "Date not specified",
    titleUnknown: "Untitled edition",
    groupTitleUnknown: "Untitled release group",
    editions: "Choose an edition",
    loadEditions: "Browse editions",
    mbid: "MusicBrainz ID",
    cacheNotice:
      "Known MusicBrainz cache entries only; this may not be a complete discography.",
  },
  ru: {
    artist: "Артист",
    dateUnknown: "Дата не указана",
    titleUnknown: "Издание без названия",
    groupTitleUnknown: "Группа релизов без названия",
    editions: "Выберите издание",
    loadEditions: "Показать издания",
    mbid: "MusicBrainz ID",
    cacheNotice:
      "Показаны только известные записи кэша MusicBrainz; это может быть не полная дискография.",
  },
} as const;

function creditNames(credits: LibraryArtistCreditResponse[]) {
  return credits
    .map((credit, index) => {
      const name = credit.credit_name || credit.artist_name || "";
      return `${name}${credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}`;
    })
    .join("");
}

function CreditNames({ credits }: { credits: LibraryArtistCreditResponse[] }) {
  const { locale } = useI18n();
  const unnamed = locale === "ru" ? "Без имени" : "Unnamed artist";
  const occurrences = new Map<string, number>();
  return (
    <span>
      {credits.map((credit, index) => {
        const identity = JSON.stringify(credit);
        const occurrence = occurrences.get(identity) ?? 0;
        occurrences.set(identity, occurrence + 1);
        const name = credit.credit_name || credit.artist_name || unnamed;
        return (
          <span key={`${identity}-${occurrence}`}>
            {credit.artist_id ? (
              <a
                href={`#/library/artists/${encodeURIComponent(credit.artist_id)}`}
              >
                {name}
              </a>
            ) : (
              name
            )}
            {credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}
          </span>
        );
      })}
    </span>
  );
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
 * fetched only after an explicit edition choice and stays visible when the
 * picker closes. The host owns rendering and every control label.
 */
export function useMissingReleaseGroupEditions(
  group: MissingReleaseGroupSummary | null,
  {
    enabled = true,
    initialPickerOpen = false,
  }: { enabled?: boolean; initialPickerOpen?: boolean } = {},
) {
  const { locale } = useI18n();
  const text = messages[locale];
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

  const selectedEditionID = active.selectedEdition?.provider_key ?? "";
  const editionsQuery = useListLibraryReleaseGroupEditions(
    groupKey,
    { limit: EDITION_PAGE_SIZE, offset: active.editionOffset },
    { query: { enabled: enabled && group !== null && active.pickerOpen } },
  );
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
  const editions =
    editionsQuery.data?.status === 200 ? editionsQuery.data.data : undefined;
  const pageEditions = editions?.editions ?? [];
  const editionOptions =
    active.selectedEdition &&
    !pageEditions.some(
      (edition) =>
        edition.provider_key === active.selectedEdition?.provider_key,
    )
      ? [active.selectedEdition, ...pageEditions]
      : pageEditions;
  const editionsError = responseError(editionsQuery);
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
    label: editionLabel(edition, text.titleUnknown),
    trackCount: edition.track_count,
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
    title: group?.title || text.groupTitleUnknown,
    credits: creditNames(group?.artists ?? []),
    originalReleaseDate: group?.original_release_date ?? null,
    pickerOpen: active.pickerOpen,
    togglePicker: () => update({ pickerOpen: !active.pickerOpen }),
    showEditionView: active.pickerOpen || selectedEditionID !== "",
    viewProps,
  };
}

/**
 * Artist-screen missing release group. The album host projects editions
 * through `useMissingReleaseGroupEditions` instead of rendering this row.
 */
export function MissingReleaseGroupRow({
  group,
}: {
  group: MissingReleaseGroupSummary;
}) {
  const { locale } = useI18n();
  const text = messages[locale];
  const projection = useMissingReleaseGroupEditions(group);
  const { pickerOpen, showEditionView, viewProps } = projection;
  const credits = creditNames(group.artists);
  const title = group.title || text.groupTitleUnknown;

  const details = (
    <div>
      <p>
        {group.original_release_date ?? text.dateUnknown}
        {credits ? (
          <>
            {" · "}
            <CreditNames credits={group.artists} />
          </>
        ) : null}
      </p>
      <p className="block text-[var(--muted)]">{text.cacheNotice}</p>
      <AppButton aria-expanded={pickerOpen} onPress={projection.togglePicker}>
        {pickerOpen ? text.editions : text.loadEditions}
      </AppButton>
      {showEditionView && (
        <div>
          <LibraryMissingEditionView locale={locale} {...viewProps} />
        </div>
      )}
    </div>
  );

  return (
    <article className="my-3 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-[10px]">
      <div className="flex flex-wrap items-center gap-2.5 max-[720px]:items-start">
        <span
          className="grid size-[46px] flex-[0_0_46px] place-items-center rounded-[5px] bg-[var(--raised)] text-[20px] font-[650] text-[var(--ink)]"
          aria-hidden="true"
        >
          {title.trim().charAt(0).toLocaleUpperCase() || "M"}
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="m-0 text-base leading-[22px]">{title}</h2>
          <p className="m-0">
            {group.original_release_date ?? text.dateUnknown} ·{" "}
            {credits ? <CreditNames credits={group.artists} /> : text.artist}
          </p>
          <p className="m-0 block text-[var(--muted)]">
            {text.mbid}: {group.provider_key}
          </p>
        </div>
      </div>
      {details}
    </article>
  );
}
