import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  useGetLibraryArtist,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import { ArtistDetailsEditor } from "./ArtistDetailsEditor";
import { ArtistSortNameEditor } from "./ArtistSortNameEditor";
import { GenreMetadataEditor } from "./GenreMetadataEditor";
import { LibraryDeletionControl } from "./LibraryDeletionControl";
import {
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryArtistRoute,
} from "./libraryRoute";
import { MissingReleaseGroupRow } from "./MissingReleaseGroupRow";
import "./catalog.css";

const PAGE_SIZE = 50;

const artistScreenCopy = {
  en: {
    backToLibrary: "← Back to library",
    loadingArtist: "Loading artist…",
    artistLoadFailed: (status: number) =>
      `Could not load artist (HTTP ${status}).`,
    retry: "Retry",
    artistReleaseSummary:
      "Includes known cached groups; the discography may be incomplete.",
    loadingReleases: "Loading releases…",
    releaseLoadFailed: (status: number) =>
      `Could not load releases (HTTP ${status}).`,
    artistReleases: "Artist releases",
    untitledRelease: "Untitled release",
    untitledPosition: "Untitled",
    dateNotSpecified: "Date not specified",
    withAudio: "with audio",
    published: "published",
    hidePositions: "Hide positions",
    showPositions: "Show positions",
    noReleasesFound: "No releases found",
    changeFilters: "Try changing the filters.",
    artistHasNoReleases: "This artist has no releases yet.",
    releasePages: "Release pages",
    of: "of",
    previous: "Previous",
    next: "Next",
    loadingPositions: "Loading positions…",
    positionLoadFailed: (status: number) =>
      `Could not load positions (HTTP ${status}).`,
    positionPages: "Position pages",
    audioAvailable: "Audio available",
    audioMissing: "Audio missing",
    unpublished: "Not published",
    noPositionsInRelease: "This release has no positions yet.",
  },
  ru: {
    backToLibrary: "← К медиатеке",
    loadingArtist: "Загрузка артиста…",
    artistLoadFailed: (status: number) =>
      `Не удалось загрузить артиста (HTTP ${status}).`,
    retry: "Повторить",
    artistReleaseSummary:
      "Включая известные группы из кэша; дискография может быть неполной.",
    loadingReleases: "Загрузка релизов…",
    releaseLoadFailed: (status: number) =>
      `Не удалось загрузить релизы (HTTP ${status}).`,
    artistReleases: "Релизы артиста",
    untitledRelease: "Релиз без названия",
    untitledPosition: "Без названия",
    dateNotSpecified: "Дата не указана",
    withAudio: "с аудио",
    published: "опубликовано",
    hidePositions: "Скрыть позиции",
    showPositions: "Показать позиции",
    noReleasesFound: "Релизы не найдены",
    changeFilters: "Попробуйте изменить фильтры.",
    artistHasNoReleases: "У этого артиста пока нет релизов.",
    releasePages: "Страницы релизов",
    of: "из",
    previous: "Назад",
    next: "Далее",
    loadingPositions: "Загрузка позиций…",
    positionLoadFailed: (status: number) =>
      `Не удалось загрузить позиции (HTTP ${status}).`,
    positionPages: "Страницы позиций",
    audioAvailable: "Аудио в наличии",
    audioMissing: "Аудио не хватает",
    unpublished: "Не опубликовано",
    noPositionsInRelease: "В этом релизе пока нет позиций.",
  },
} as const;

function countLabel(
  count: number,
  locale: "en" | "ru",
  forms: {
    en: readonly [string, string];
    ru: readonly [string, string, string];
  },
) {
  const formatted = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);
  if (locale === "en") return `${formatted} ${forms.en[count === 1 ? 0 : 1]}`;
  const plural = new Intl.PluralRules("ru-RU").select(count);
  return `${formatted} ${forms.ru[plural === "one" ? 0 : plural === "few" ? 1 : 2]}`;
}

export function LibraryArtistScreen({ artistId }: { artistId: string }) {
  const { locale } = useI18n();
  const copy = artistScreenCopy[locale];
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
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [artistMetadataRefreshToken, setArtistMetadataRefreshToken] =
    useState(0);
  function invalidateArtistMetadata() {
    setArtistMetadataRefreshToken((generation) => generation + 1);
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
          copy.artistLoadFailed(artistQuery.data.status),
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
          copy.releaseLoadFailed(releasesQuery.data.status),
        ).message
      : releasesQuery.error instanceof Error
        ? releasesQuery.error.message
        : "";
  const releases =
    releasesQuery.data?.status === 200 ? releasesQuery.data.data : undefined;

  return (
    <main className="library-catalog" aria-labelledby="artist-title">
      <a className="catalog-back" href={libraryRouteHref(route)}>
        {copy.backToLibrary}
      </a>
      {artistQuery.isLoading ? (
        <p role="status">{copy.loadingArtist}</p>
      ) : error ? (
        <section className="catalog-state catalog-state--error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={() => void artistQuery.refetch()}>
            {copy.retry}
          </button>
        </section>
      ) : artist ? (
        <>
          <header className="catalog-header">
            <div>
              <ArtistDetailsEditor
                artistId={artistId}
                fallbackName={artist.name}
                fallbackDisambiguation={artist.disambiguation}
                refreshToken={artistMetadataRefreshToken}
                onSaved={invalidateArtistMetadata}
              />
              <p className="catalog-muted">
                {countLabel(artist.release_count, locale, {
                  en: ["release", "releases"],
                  ru: ["релиз", "релиза", "релизов"],
                })}{" "}
                ·{" "}
                {countLabel(artist.position_count, locale, {
                  en: ["position", "positions"],
                  ru: ["позиция", "позиции", "позиций"],
                })}{" "}
                · {copy.withAudio}: {artist.available_position_count} ·{" "}
                {artist.published_position_count} {copy.published}
              </p>
              <p className="catalog-muted">{copy.artistReleaseSummary}</p>
              <LibraryDeletionControl
                target={{ type: "artist", artistId }}
                releaseTitle={artist.name}
                onSnapshot={handleArtistDeletionSnapshot}
              />
            </div>
            <div className="catalog-genre-list">
              {artist.genres.map((genre: string) => (
                <span key={genre}>{genre}</span>
              ))}
            </div>
          </header>
          <ArtistSortNameEditor
            artistId={artistId}
            refreshToken={artistMetadataRefreshToken}
            onSaved={invalidateArtistMetadata}
          />
          <GenreMetadataEditor
            owner="artist"
            ownerId={artistId}
            refreshToken={artistMetadataRefreshToken}
            onSaved={invalidateArtistMetadata}
          />
          {releasesQuery.isLoading ? (
            <p role="status">{copy.loadingReleases}</p>
          ) : releasesError ? (
            <section
              className="catalog-state catalog-state--error"
              role="alert"
            >
              <p>{releasesError}</p>
              <button
                type="button"
                onClick={() => void releasesQuery.refetch()}
              >
                {copy.retry}
              </button>
            </section>
          ) : releases?.items.length ? (
            <section
              className="catalog-artist-releases"
              aria-label={copy.artistReleases}
            >
              {releases.items.map((item) => {
                if (
                  item.kind === "missing_release_group" &&
                  item.missing_release_group
                ) {
                  return (
                    <MissingReleaseGroupRow
                      key={`missing_release_group:${item.missing_release_group.provider_key}`}
                      group={item.missing_release_group}
                      presentation="artist"
                    />
                  );
                }
                const release = item.library_release;
                if (!release) return null;
                return (
                  <article
                    className="catalog-artist-release"
                    key={`library_release:${release.id}`}
                  >
                    <div className="catalog-release-heading">
                      <span className="catalog-initial" aria-hidden="true">
                        {release.title.trim().charAt(0).toLocaleUpperCase() ||
                          "M"}
                      </span>
                      <div>
                        <h2>
                          <a
                            href={`#/library/releases/${encodeURIComponent(release.id)}`}
                          >
                            {release.title || copy.untitledRelease}
                          </a>
                        </h2>
                        <p>
                          {release.edition.release_date ??
                            release.edition.original_release_date ??
                            copy.dateNotSpecified}{" "}
                          ·{" "}
                          {countLabel(release.counts.position_count, locale, {
                            en: ["position", "positions"],
                            ru: ["позиция", "позиции", "позиций"],
                          })}{" "}
                          · {release.counts.available_position_count}{" "}
                          {copy.withAudio} ·{" "}
                          {release.counts.published_position_count}{" "}
                          {copy.published}
                        </p>
                      </div>
                      <button
                        type="button"
                        aria-expanded={Boolean(expanded[release.id])}
                        onClick={() =>
                          setExpanded((value) => ({
                            ...value,
                            [release.id]: !value[release.id],
                          }))
                        }
                      >
                        {expanded[release.id]
                          ? copy.hidePositions
                          : copy.showPositions}
                      </button>
                    </div>
                    {expanded[release.id] && (
                      <ReleasePositions
                        releaseId={release.id}
                        locale={locale}
                      />
                    )}
                  </article>
                );
              })}
            </section>
          ) : (
            <section className="catalog-state">
              <h2>{copy.noReleasesFound}</h2>
              <p>
                {route.query ||
                route.publication !== "all" ||
                route.availability !== "all"
                  ? copy.changeFilters
                  : copy.artistHasNoReleases}
              </p>
            </section>
          )}
          {releases && releases.total > PAGE_SIZE && (
            <nav className="catalog-pagination" aria-label={copy.releasePages}>
              <span>
                {route.offset + 1}–
                {Math.min(route.offset + PAGE_SIZE, releases.total)} {copy.of}{" "}
                {releases.total}
              </span>
              <a
                aria-disabled={route.offset === 0}
                href={libraryArtistRouteHref(artistId, {
                  ...route,
                  offset: Math.max(0, route.offset - PAGE_SIZE),
                })}
              >
                {copy.previous}
              </a>
              <a
                aria-disabled={route.offset + PAGE_SIZE >= releases.total}
                href={libraryArtistRouteHref(artistId, {
                  ...route,
                  offset: route.offset + PAGE_SIZE,
                })}
              >
                {copy.next}
              </a>
            </nav>
          )}
        </>
      ) : null}
    </main>
  );
}

function ReleasePositions({
  releaseId,
  locale,
}: {
  releaseId: string;
  locale: "en" | "ru";
}) {
  const copy = artistScreenCopy[locale];
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
      ? apiResponseError(query.data, copy.positionLoadFailed(query.data.status))
          .message
      : query.error instanceof Error
        ? query.error.message
        : "";
  if (query.isLoading) return <p role="status">{copy.loadingPositions}</p>;
  if (failure)
    return (
      <section className="catalog-state catalog-state--error" role="alert">
        <p>{failure}</p>
        <button type="button" onClick={() => void query.refetch()}>
          {copy.retry}
        </button>
      </section>
    );
  const page = query.data?.status === 200 ? query.data.data : undefined;
  const positions = page?.items ?? [];
  return positions.length ? (
    <>
      <ol className="catalog-position-list">
        {positions.map((position) => (
          <li key={position.id}>
            <a
              href={`#/library/releases/${encodeURIComponent(releaseId)}/positions/${encodeURIComponent(position.id)}`}
            >
              {position.medium}.{position.displayed_number || position.position}{" "}
              {position.title || copy.untitledPosition}
            </a>
            <span>
              {position.audio_available
                ? copy.audioAvailable
                : copy.audioMissing}
              {" · "}
              {countLabel(position.source_variant_count, locale, {
                en: ["source", "sources"],
                ru: ["исходник", "исходника", "исходников"],
              })}{" "}
              · {position.publication ? copy.published : copy.unpublished}
            </span>
          </li>
        ))}
      </ol>
      {page && page.total > PAGE_SIZE && (
        <nav className="catalog-pagination" aria-label={copy.positionPages}>
          <span>
            {offset + 1}–{Math.min(offset + PAGE_SIZE, page.total)} {copy.of}{" "}
            {page.total}
          </span>
          <button
            type="button"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
          >
            {copy.previous}
          </button>
          <button
            type="button"
            disabled={offset + PAGE_SIZE >= page.total}
            onClick={() => setOffset(offset + PAGE_SIZE)}
          >
            {copy.next}
          </button>
        </nav>
      )}
    </>
  ) : (
    <p className="catalog-muted">{copy.noPositionsInRelease}</p>
  );
}
