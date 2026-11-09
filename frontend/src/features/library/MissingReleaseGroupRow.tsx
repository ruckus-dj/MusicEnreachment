import { useState } from "react";
import { apiResponseError } from "../../api/client/errors";
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

const EDITION_PAGE_SIZE = 50;

const messages = {
  en: {
    artist: "Artist",
    dateUnknown: "Date not specified",
    titleUnknown: "Untitled edition",
    groupTitleUnknown: "Untitled release group",
    editions: "Choose an edition",
    chooseEdition: "Choose an edition to inspect",
    loadEditions: "Browse editions",
    loadingEditions: "Loading editions…",
    editionsError: "Could not load MusicBrainz editions.",
    retry: "Retry",
    trackCount: "Tracks",
    noTrackCount: "—",
    loadingTracks: "Loading track list…",
    tracksError: "Could not load this edition’s track list.",
    noTracks: "No tracks are listed for this edition.",
    partialCache:
      "The cached track list is incomplete; it may not represent the full edition.",
    unknownCache: "Track-list completeness is unknown.",
    page: "Page",
    previous: "Previous editions",
    next: "More editions",
    emptyEditionPage: "No editions on this page.",
    mbid: "MusicBrainz ID",
    missingFromLibrary: "No library entry",
    cacheNotice:
      "Known MusicBrainz cache entries only; this may not be a complete discography.",
    noKnownEditions: "No editions are known in the local cache.",
  },
  ru: {
    artist: "Артист",
    dateUnknown: "Дата не указана",
    titleUnknown: "Издание без названия",
    groupTitleUnknown: "Группа релизов без названия",
    editions: "Выберите издание",
    chooseEdition: "Выберите издание для просмотра",
    loadEditions: "Показать издания",
    loadingEditions: "Загрузка изданий…",
    editionsError: "Не удалось загрузить издания MusicBrainz.",
    retry: "Повторить",
    trackCount: "Треков",
    noTrackCount: "—",
    loadingTracks: "Загрузка списка треков…",
    tracksError: "Не удалось загрузить список треков этого издания.",
    noTracks: "Для этого издания треки не указаны.",
    partialCache:
      "Кэшированный список неполный и может не включать все треки издания.",
    unknownCache: "Полнота списка треков неизвестна.",
    page: "Страница",
    previous: "Предыдущие издания",
    next: "Следующие издания",
    emptyEditionPage: "На этой странице нет изданий.",
    mbid: "MusicBrainz ID",
    missingFromLibrary: "В медиатеке отсутствует",
    cacheNotice:
      "Показаны только известные записи кэша MusicBrainz; это может быть не полная дискография.",
    noKnownEditions: "В локальном кэше нет известных изданий.",
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
  const occurrences = new Map<string, number>();
  return (
    <span>
      {credits.map((credit, index) => {
        const identity = JSON.stringify(credit);
        const occurrence = occurrences.get(identity) ?? 0;
        occurrences.set(identity, occurrence + 1);
        const name = credit.credit_name || credit.artist_name || "Без имени";
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

function durationLabel(duration: number | null) {
  if (duration === null) return "—";
  const seconds = Math.floor(duration / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function responseError(
  query: { data?: { status: number; data?: unknown }; error?: unknown },
  fallback: string,
) {
  if (query.data?.status !== undefined && query.data.status !== 200) {
    return apiResponseError(query.data, fallback).message;
  }
  return query.error instanceof Error ? query.error.message : "";
}

export function MissingReleaseGroupRow({
  group,
  presentation,
}: {
  group: MissingReleaseGroupSummary;
  presentation: "table" | "artist";
}) {
  const { locale } = useI18n();
  const text = messages[locale];
  const [pickerOpen, setPickerOpen] = useState(false);
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
  const editionsError = responseError(editionsQuery, text.editionsError);
  const tracksError = responseError(tracksQuery, text.tracksError);
  const trackList =
    tracksQuery.data?.status === 200 ? tracksQuery.data.data : undefined;
  const credits = creditNames(group.artists);
  const title = group.title || text.groupTitleUnknown;

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
      <button
        type="button"
        aria-expanded={pickerOpen}
        onClick={() => setPickerOpen((open) => !open)}
      >
        {pickerOpen ? text.editions : text.loadEditions}
      </button>
      {pickerOpen && (
        <div>
          {editionsQuery.isLoading ? (
            <p role="status">{text.loadingEditions}</p>
          ) : editionsError ? (
            <section
              className="catalog-state catalog-state--error"
              role="alert"
            >
              <p>{editionsError}</p>
              <button
                type="button"
                onClick={() => void editionsQuery.refetch()}
              >
                {text.retry}
              </button>
            </section>
          ) : editions?.total === 0 ? (
            <p className="catalog-muted">{text.noKnownEditions}</p>
          ) : editions ? (
            <>
              {pageEditions.length === 0 && editionOptions.length === 0 ? (
                <p className="catalog-muted">{text.emptyEditionPage}</p>
              ) : (
                <label>
                  {text.editions}
                  <select
                    value={selectedEditionID}
                    onChange={(event) => {
                      const editionID = event.currentTarget.value;
                      setSelectedEdition(
                        editionOptions.find(
                          (edition) => edition.provider_key === editionID,
                        ) ?? null,
                      );
                    }}
                  >
                    <option value="">{text.chooseEdition}</option>
                    {editionOptions.map((edition) => (
                      <option
                        key={edition.provider_key}
                        value={edition.provider_key}
                      >
                        {editionLabel(edition, text.titleUnknown)}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {(editionOffset > 0 || editions.total > EDITION_PAGE_SIZE) && (
                <nav className="catalog-pagination" aria-label={text.editions}>
                  <span>
                    {text.page}{" "}
                    {Math.floor(editionOffset / EDITION_PAGE_SIZE) + 1}
                  </span>
                  <button
                    type="button"
                    disabled={editionOffset === 0}
                    onClick={() =>
                      setEditionOffset(
                        Math.max(0, editionOffset - EDITION_PAGE_SIZE),
                      )
                    }
                  >
                    {text.previous}
                  </button>
                  <button
                    type="button"
                    disabled={
                      editionOffset + EDITION_PAGE_SIZE >= editions.total
                    }
                    onClick={() =>
                      setEditionOffset(editionOffset + EDITION_PAGE_SIZE)
                    }
                  >
                    {text.next}
                  </button>
                </nav>
              )}
            </>
          ) : null}
        </div>
      )}
      {selectedEditionID && (
        <section
          aria-label={
            selectedEdition
              ? editionLabel(selectedEdition, text.titleUnknown)
              : text.editions
          }
        >
          <p>
            {text.trackCount}:{" "}
            {trackList?.track_count ??
              selectedEdition?.track_count ??
              text.noTrackCount}
          </p>
          {tracksQuery.isLoading ? (
            <p role="status">{text.loadingTracks}</p>
          ) : tracksError ? (
            <section
              className="catalog-state catalog-state--error"
              role="alert"
            >
              <p>{tracksError}</p>
              <button type="button" onClick={() => void tracksQuery.refetch()}>
                {text.retry}
              </button>
            </section>
          ) : trackList ? (
            <>
              {trackList.complete === false && (
                <p className="catalog-muted">{text.partialCache}</p>
              )}
              {trackList.complete === null && (
                <p className="catalog-muted">{text.unknownCache}</p>
              )}
              {trackList.tracks.length ? (
                <ol className="catalog-position-list">
                  {trackList.tracks.map((track) => (
                    <li key={`${track.medium}:${track.position}`}>
                      <span>
                        {track.medium}.{track.number} {track.title}
                      </span>
                      <span>
                        {track.recording_title ?? ""}
                        {track.recording_title ? " · " : ""}
                        {durationLabel(track.duration_ms)}
                      </span>
                    </li>
                  ))}
                </ol>
              ) : (
                <p className="catalog-muted">{text.noTracks}</p>
              )}
            </>
          ) : null}
        </section>
      )}
    </div>
  );

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
