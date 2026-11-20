import { useState } from "react";
import {
  useGetLibraryReleaseGroupEditionTracks,
  useListLibraryReleaseGroupEditions,
} from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryReleaseGroupEditionResponse,
  MissingReleaseGroupSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type LibraryEditionPagination,
  type LibraryEditionsStatus,
  type LibraryEditionTracks,
  type LibraryEditionTracksStatus,
  LibraryMissingEditionView,
} from "./LibraryMissingEditionView";

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
    missingFromLibrary: "No library entry",
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
    missingFromLibrary: "В медиатеке отсутствует",
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

export function MissingReleaseGroupRow({
  group,
  presentation,
}: {
  group: MissingReleaseGroupSummary;
  presentation: "table" | "artist" | "inline";
}) {
  const { locale } = useI18n();
  const text = messages[locale];
  const [pickerOpen, setPickerOpen] = useState(presentation === "inline");
  const [editionOffset, setEditionOffset] = useState(0);
  const [selectedEdition, setSelectedEdition] =
    useState<LibraryReleaseGroupEditionResponse | null>(null);
  const selectedEditionID = selectedEdition?.provider_key ?? "";
  const editionsQuery = useListLibraryReleaseGroupEditions(
    group.provider_key,
    { limit: EDITION_PAGE_SIZE, offset: editionOffset },
    { query: { enabled: pickerOpen } },
  );
  const tracksQuery = useGetLibraryReleaseGroupEditionTracks(
    group.provider_key,
    selectedEditionID,
    {
      query: {
        enabled: selectedEditionID !== "",
        queryKey: [
          "/api/library/release-groups",
          group.provider_key,
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
    selectedEdition &&
    !pageEditions.some(
      (edition) => edition.provider_key === selectedEdition.provider_key,
    )
      ? [selectedEdition, ...pageEditions]
      : pageEditions;
  const editionsError = responseError(editionsQuery);
  const tracksError = responseError(tracksQuery);
  const trackList =
    tracksQuery.data?.status === 200 ? tracksQuery.data.data : undefined;
  const credits = creditNames(group.artists);
  const title = group.title || text.groupTitleUnknown;

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
    editions && (editionOffset > 0 || editions.total > EDITION_PAGE_SIZE)
      ? {
          page: Math.floor(editionOffset / EDITION_PAGE_SIZE) + 1,
          hasPrevious: editionOffset > 0,
          hasNext: editionOffset + EDITION_PAGE_SIZE < editions.total,
          onPrevious: () =>
            setEditionOffset(Math.max(0, editionOffset - EDITION_PAGE_SIZE)),
          onNext: () => setEditionOffset(editionOffset + EDITION_PAGE_SIZE),
        }
      : null;

  const details = (
    <div className="catalog-missing-release-details">
      <p>
        {group.original_release_date ?? text.dateUnknown}
        {credits ? (
          <>
            {" · "}
            <CreditNames credits={group.artists} />
          </>
        ) : null}
      </p>
      <p className="catalog-muted">{text.cacheNotice}</p>
      {presentation !== "inline" && (
        <button
          type="button"
          aria-expanded={pickerOpen}
          onClick={() => setPickerOpen((open) => !open)}
        >
          {pickerOpen ? text.editions : text.loadEditions}
        </button>
      )}
      {(pickerOpen || selectedEditionID !== "") && (
        <div>
          <LibraryMissingEditionView
            locale={locale}
            pickerVisible={pickerOpen}
            editions={editionChoices}
            editionsStatus={editionsStatus}
            editionsError={editionsError}
            onRetryEditions={() => void editionsQuery.refetch()}
            selectedEditionKey={selectedEditionID}
            onSelectEdition={(editionKey) =>
              setSelectedEdition(
                editionOptions.find(
                  (edition) => edition.provider_key === editionKey,
                ) ?? null,
              )
            }
            pagination={pagination}
            tracksStatus={tracksStatus}
            tracksError={tracksError}
            onRetryTracks={() => void tracksQuery.refetch()}
            tracks={tracks}
          />
        </div>
      )}
    </div>
  );

  if (presentation === "inline") return details;
  if (presentation === "table") {
    return (
      <tr className="catalog-missing-release" aria-label={title}>
        <td>
          <span className="catalog-entity">
            <span className="catalog-cover" aria-hidden="true">
              {title.trim().charAt(0).toLocaleUpperCase() || "M"}
            </span>
            <strong>{title}</strong>
          </span>
          <span className="catalog-sub">
            {text.mbid}: {group.provider_key}
          </span>
          {details}
        </td>
        <td>{credits ? <CreditNames credits={group.artists} /> : "—"}</td>
        <td>{group.original_release_date ?? text.dateUnknown}</td>
        <td>—</td>
        <td>{text.missingFromLibrary}</td>
      </tr>
    );
  }

  return (
    <article className="catalog-artist-release catalog-missing-release">
      <div className="catalog-release-heading">
        <span className="catalog-initial" aria-hidden="true">
          {title.trim().charAt(0).toLocaleUpperCase() || "M"}
        </span>
        <div>
          <h2>{title}</h2>
          <p>
            {group.original_release_date ?? text.dateUnknown} ·{" "}
            {credits ? <CreditNames credits={group.artists} /> : text.artist}
          </p>
          <p className="catalog-muted">
            {text.mbid}: {group.provider_key}
          </p>
        </div>
      </div>
      {details}
    </article>
  );
}
